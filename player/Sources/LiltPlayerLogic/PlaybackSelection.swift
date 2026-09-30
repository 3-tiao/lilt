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

// MusicKit's play() can return before its media starts. A response may claim
// playback only after the selected entry's position advances while MusicKit
// reports playing; a queue assignment or a transient playing flag is not proof.
public func musicStartConfirmed(status: String, previousPosition: Double?, position: Double,
                                currentSongID: String?, expectedSongID: String?,
                                currentEntryID: String? = nil, previousEntryID: String? = nil,
                                sameQueueSong: Bool = false) -> Bool {
    guard status == "playing", let previousPosition, position > previousPosition + 0.02 else { return false }
    if let previousEntryID, (currentEntryID == nil || currentEntryID == previousEntryID) { return false }
    return expectedSongID == nil || currentSongID == expectedSongID || sameQueueSong
}

// A timeout needs to say which start condition did not settle. Keep only
// bounded observations: no song IDs, titles, entry IDs, or media URLs can
// escape through the helper error, public details, or journal.
public struct MusicStartDiagnostics: Equatable, Sendable {
    public private(set) var samples = 0
    public private(set) var playingSamples = 0
    public private(set) var advancingSamples = 0
    public private(set) var playingAdvanceSamples = 0
    public private(set) var wrongSongSamples = 0
    public private(set) var missingSongSamples = 0
    public private(set) var unchangedEntrySamples = 0
    public private(set) var lastStatus = "unknown"
    public private(set) var lastSongMatch = "unknown"
    public private(set) var lastPosition = 0.0

    public init() {}

    public mutating func observe(status: String, previousPosition: Double?, position: Double,
                                 currentSongID: String?, expectedSongID: String?,
                                 currentEntryID: String?, previousEntryID: String?) {
        samples += 1
        switch status {
        case "playing", "paused", "stopped": lastStatus = status
        default: lastStatus = "other"
        }
        lastPosition = position.isFinite ? min(86_400, max(0, position)) : 0
        if expectedSongID == nil { lastSongMatch = "not_checked" }
        else if currentSongID == nil { lastSongMatch = "missing" }
        else { lastSongMatch = currentSongID == expectedSongID ? "yes" : "no" }
        if status == "playing" { playingSamples += 1 }
        let advancing = previousPosition.map { position > $0 + 0.02 } ?? false
        if advancing { advancingSamples += 1 }
        guard status == "playing" && advancing else { return }
        playingAdvanceSamples += 1
        if lastSongMatch == "no" { wrongSongSamples += 1 }
        if lastSongMatch == "missing" { missingSongSamples += 1 }
        if let previousEntryID, (currentEntryID == nil || currentEntryID == previousEntryID) {
            unchangedEntrySamples += 1
        }
    }

    public func summary(queueEntries: Int) -> String {
        "startDiagnostic=samples:\(samples),playing:\(playingSamples),advancing:\(advancingSamples)," +
        "playingAdvance:\(playingAdvanceSamples),wrongSong:\(wrongSongSamples)," +
        "missingSong:\(missingSongSamples),unchangedEntry:\(unchangedEntrySamples)," +
        "lastStatus:\(lastStatus),lastSongMatch:\(lastSongMatch)," +
        "lastPosition:\(String(format: "%.1f", lastPosition)),queueEntries:\(min(10_000, max(0, queueEntries)))"
    }
}

// MediaTimeControl mirrors AVPlayer.TimeControlStatus without importing
// AVFoundation, so the status contract stays testable.
public enum MediaTimeControl: Equatable, Sendable {
    case playing
    case waiting
    case paused
}

// mediaSessionStatus maps an AVFoundation session onto the helper's wire status.
//
// The rule that matters: a session the client did not pause is NEVER reported as
// "paused". AVPlayer also lands in .paused when a stream stops making progress
// (a dead media URL reports no error at all), and "paused" tells the server the
// session is resting, so its stall watchdog stops watching and the track freezes
// silently. "paused" is reserved for a pause that was actually requested — by a
// client RPC or by a system media key — so that a real pause is never restarted
// either.
public func mediaSessionStatus(mode: String, ended: Bool, pauseRequested: Bool, timeControl: MediaTimeControl) -> String {
    if mode == "none" || ended { return "stopped" }
    if pauseRequested { return "paused" }
    switch timeControl {
    case .playing: return "playing"
    case .waiting, .paused: return "buffering"
    }
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

// MusicKit can expose a catalog Song while the submitted queue holds library
// Songs. Entry ids can change as playback advances, so neither an entry id nor
// a previous index identifies the current song. Only an unambiguous Song id or
// a unique metadata match can locate it in the canonical submitted order.
public struct QueueSongIdentity: Equatable, Sendable {
    public let id: String
    public let title: String
    public let artist: String
    public let album: String?
    public let duration: Double?

    public init(id: String, title: String, artist: String, album: String?, duration: Double?) {
        self.id = id
        self.title = title
        self.artist = artist
        self.album = album
        self.duration = duration
    }
}

public func canonicalQueueIndex(songs: [QueueSongIdentity], current: QueueSongIdentity?) -> Int? {
    guard let current else { return nil }
    let idMatches = songs.indices.filter { songs[$0].id == current.id }
    if idMatches.count == 1 { return idMatches[0] }
    // A duplicate id cannot identify an occurrence, even when the metadata is
    // the same. Do not claim an index based on the last played row.
    if !idMatches.isEmpty { return nil }
    guard !current.title.isEmpty, !current.artist.isEmpty else { return nil }
    let matches = songs.indices.filter { index in
        let song = songs[index]
        guard song.title == current.title, song.artist == current.artist else { return false }
        if let album = song.album, let currentAlbum = current.album, album != currentAlbum { return false }
        if let duration = song.duration, let currentDuration = current.duration,
           abs(duration - currentDuration) > 2 { return false }
        return true
    }
    return matches.count == 1 ? matches[0] : nil
}

// A MusicKit currentEntry can carry a catalog ID while the submitted playlist
// carries a library ID for the same song. Confirm an alternate identity only
// when both the requested row and the live row map uniquely to that row.
// Never use the previous cursor or the position of a shuffled live entry.
public func startTargetMatches(songs: [QueueSongIdentity], expectedSongID: String?,
                               current: QueueSongIdentity?) -> Bool {
    guard let expectedSongID, let current else { return false }
    let matches = songs.indices.filter { songs[$0].id == expectedSongID }
    guard matches.count == 1 else { return false }
    return canonicalQueueIndex(songs: songs, current: current) == matches[0]
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

public func restoredQueue<T>(_ items: [T], item: T, at index: Int) -> [T]? {
    guard index >= 0, index <= items.count else { return nil }
    var result = items
    result.insert(item, at: index)
    return result
}

public func queueUndoContextMatches(postRemove: [QueueSongIdentity], currentQueue: [QueueSongIdentity],
                                    removedIndex: Int, expectedCurrentIndex: Int,
                                    currentIndex: Int?) -> Bool {
    postRemove == currentQueue && currentIndex == expectedCurrentIndex &&
        removedIndex > expectedCurrentIndex && removedIndex <= currentQueue.count
}

public func movedQueue<T>(_ items: [T], from: Int, to: Int) -> [T]? {
    guard items.indices.contains(from), to >= 0, to < items.count else { return nil }
    var result = items
    let item = result.remove(at: from)
    result.insert(item, at: to)
    return result
}

// liveReorder mirrors how queueMove edits MusicKit's live queue: remove the
// moved entry, then insert it at `to` clamped to the resulting count. It exists
// so the current-entry restoration decision below and the helper's own edit use
// one definition.
public func liveReorder<T>(_ items: [T], from: Int, to: Int) -> [T]? {
    guard items.indices.contains(from), to >= 0 else { return nil }
    var result = items
    let item = result.remove(at: from)
    result.insert(item, at: min(to, result.count))
    return result
}

// queueMoveCurrentRestore answers where the entry at currentIndex ends up after
// a live reorder, or nil when its row does not move. Reassigning
// player.queue.entries makes MusicKit resolve the current entry by live index,
// so a move that crosses that row switches playback and restarts it from 0:00.
// The calculation is positional: Song payload IDs may be duplicated and
// MusicKit regenerates Queue.Entry IDs on every assignment. When this returns
// an index the helper must walk the current entry back to that row.
public func queueMoveCurrentRestore(currentIndex: Int, entryCount: Int, from: Int, to: Int) -> Int? {
    let indices = Array(0..<entryCount)
    guard indices.indices.contains(currentIndex),
          let reordered = liveReorder(indices, from: from, to: to),
          let after = reordered.firstIndex(of: currentIndex), after != currentIndex else { return nil }
    return after
}
