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
// queue order. MusicKit rebuilds Queue.Entry ids whenever a queue is assigned or
// advances, so the entry's Song payload id is the only stable link to the
// canonical submitted order.
public func canonicalQueueIndex(ids: [String], currentSongID: String?, fallbackIndex: Int) -> Int {
    guard !ids.isEmpty else { return 0 }
    if let currentSongID, let index = ids.firstIndex(of: currentSongID) { return index }
    return min(max(fallbackIndex, 0), ids.count - 1)
}

// liveEntryOffset locates the live MusicKit entry that holds a canonical queue
// position. The positional entry is preferred when it already holds the
// expected song, so duplicate songs stay distinguishable; otherwise the first
// entry with that song id matches. When neither is available the canonical
// position is used, which keeps index-based edits working on a queue the helper
// cannot resolve.
public func liveEntryOffset(entrySongIDs: [String?], songID: String, canonicalIndex: Int) -> Int? {
    if entrySongIDs.indices.contains(canonicalIndex), entrySongIDs[canonicalIndex] == songID { return canonicalIndex }
    if let match = entrySongIDs.firstIndex(where: { $0 == songID }) { return match }
    return entrySongIDs.indices.contains(canonicalIndex) ? canonicalIndex : nil
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
