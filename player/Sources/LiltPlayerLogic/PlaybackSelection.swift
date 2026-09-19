public struct StartTrack: Equatable, Sendable {
    public let id: String?

    public init(id: String?) {
        self.id = id
    }
}

public func selectedStartIndex(tracks: [StartTrack], id: String?, index: Int?) -> Int {
    guard !tracks.isEmpty else { return 0 }
    if let id, !id.isEmpty, let match = tracks.firstIndex(where: { $0.id == id }) { return match }
    if let index, tracks.indices.contains(index) { return index }
    return 0
}

public struct PlaylistEntryDescriptor: Equatable, Sendable {
    public let kind: String
    public let id: String?
    public let title: String
    public let available: Bool
    public let originalIndex: Int

    public init(kind: String, id: String?, title: String, available: Bool = true, originalIndex: Int) {
        self.kind = kind
        self.id = id
        self.title = title
        self.available = available
        self.originalIndex = originalIndex
    }
}

// supportedPlaylistEntries models the browse/play contract independently of
// MusicKit: only available songs enter either surface, while their source index
// remains available for diagnostics and stable mapping.
public func supportedPlaylistEntries(_ entries: [PlaylistEntryDescriptor], reverse: Bool = false) -> [PlaylistEntryDescriptor] {
    var supported = entries.filter { $0.available && $0.kind == "song" }
    if reverse { supported.reverse() }
    return supported
}

public func musicAccountStatus(canPlayCatalogContent: Bool, hasCloudLibraryEnabled: Bool) -> String {
    if !canPlayCatalogContent { return "subscription_required" }
    if !hasCloudLibraryEnabled { return "cloud_library_disabled" }
    return "ready"
}

// probeErrorCode maps an AVFoundation/URL loading failure to the stable probe
// error vocabulary the UI displays and localizes.
public func probeErrorCode(domain: String, code: Int) -> String {
    switch domain {
    case "NSURLErrorDomain":
        switch code {
        case -1200, -1201, -1202, -1203, -1204, -1205, -1206: return "tls"
        case -1001: return "timeout"
        case -1002, -1003, -1004, -1005, -1008, -1009: return "network"
        default: return "network"
        }
    case "AVFoundationErrorDomain":
        switch code {
        case -11828: return "unsupported"
        default: return "unknown"
        }
    default:
        return "unknown"
    }
}

// probeHTTPResponseOutcome keeps response classification independent of the
// URLSession delegate, so the probe's wire-level contract is testable without
// making a network request.
public enum ProbeHTTPResponseOutcome: Equatable, Sendable {
    case healthy
    case httpError
    case closedWithoutData
}

public func probeHTTPResponseOutcome(statusCode: Int, receivedData: Bool) -> ProbeHTTPResponseOutcome {
    if statusCode >= 400 { return .httpError }
    if receivedData { return .healthy }
    return .closedWithoutData
}

// urlEndedApplies prevents a late AVFoundation callback from a replaced URL
// item from advancing the server-owned queue.
public func urlEndedApplies(activeGeneration: UInt64, activeSession: String, callbackGeneration: UInt64, callbackSession: String) -> Bool {
    activeGeneration == callbackGeneration && !activeSession.isEmpty && activeSession == callbackSession
}

// The queue contract has exactly one index space: the submitted song order.
// MusicKit's shuffle reorders its live entries array, so reported queue data
// and jump/remove/move indices all refer to the canonical list; shuffle only
// steers playback progression, never the reported order.
public func canonicalQueue(ids: [String], currentID: String?) -> (queue: [String], index: Int) {
    guard !ids.isEmpty else { return ([], 0) }
    var index = 0
    if let currentID, !currentID.isEmpty, let match = ids.firstIndex(of: currentID) { index = match }
    return (ids, index)
}

// canonicalQueueIndex resolves the current item without interpreting the live
// queue order. MusicKit's Queue.Entry.id is local but stable for one submitted
// queue, so it is the primary mapping; a Song id remains a fallback for queue
// entries that predate the mapping.
public func canonicalQueueIndex(ids: [String], currentEntryID: String?, entryIndices: [String: Int], currentSongID: String?, fallbackIndex: Int) -> Int {
    guard !ids.isEmpty else { return 0 }
    if let currentEntryID, let index = entryIndices[currentEntryID], ids.indices.contains(index) { return index }
    if let currentSongID, let index = ids.firstIndex(of: currentSongID) { return index }
    return min(max(fallbackIndex, 0), ids.count - 1)
}

public func queueTargetID(ids: [String], index: Int) -> String? {
    guard ids.indices.contains(index) else { return nil }
    return ids[index]
}

public func removedQueue<T>(_ items: [T], at index: Int) -> [T]? {
    guard items.indices.contains(index) else { return nil }
    var result = items
    result.remove(at: index)
    return result
}

public func movedQueue<T>(_ items: [T], from: Int, to: Int) -> [T]? {
    guard items.indices.contains(from), to >= 0, to < items.count else { return nil }
    var result = items
    let item = result.remove(at: from)
    result.insert(item, at: to)
    return result
}

public struct InsertedEntry: Equatable, Sendable {
    public let id: String
    public let songID: String?

    public init(id: String, songID: String?) {
        self.id = id
        self.songID = songID
    }
}

// insertedEntryIndices remaps MusicKit entry ids to canonical queue positions
// after an insert. Registered entries keep their canonical position (shifted
// past the insertion point); a newly seen entry resolves its position from its
// song id and otherwise lands at the insertion point. Assigning the insertion
// point to every unseen entry would misplace pre-existing entries that were
// never registered — for example the entry that was already playing when the
// single-song play path seeded the queue.
public func insertedEntryIndices(
    existing: [String: Int],
    insertAt: Int,
    entries: [InsertedEntry],
    canonicalSongIDs: [String],
) -> [String: Int] {
    var result: [String: Int] = [:]
    for (id, value) in existing {
        result[id] = value >= insertAt ? value + 1 : value
    }
    var known = Set(existing.keys)
    var offset = 0
    for entry in entries where !known.contains(entry.id) {
        var position = insertAt + offset
        if let songID = entry.songID, let canonical = canonicalSongIDs.firstIndex(of: songID) {
            position = canonical
        }
        result[entry.id] = position
        known.insert(entry.id)
        offset += 1
    }
    return result
}
