// PlaybackProbe is the observation record used to diagnose MusicKit playback
// behaviour from real sessions (see docs/product/open-questions.md OQ11/OQ16).
// It exists because "playing then not playing" is ambiguous from the public
// status alone: a natural end, a user pause, and a system audio arbitration
// pause all look alike, and the position can keep advancing while paused.
//
// The formatting lives here, away from MusicKit, so the line shape can be unit
// tested and the field set cannot drift silently.
public struct PlaybackProbe: Equatable {
    /// Raw MusicKit playbackStatus, before lilt's stall inference.
    public var rawStatus: String
    /// What lilt reports publicly (may be "buffering" while MusicKit says playing).
    public var mappedStatus: String
    /// Identity of the current entry, or "" when MusicKit reports none.
    public var entryID: String
    /// Position and duration in seconds.
    public var position: Double
    public var duration: Double
    /// Canonical queue index and queue sizes as lilt sees them.
    public var index: Int
    public var songs: Int
    /// MusicKit's own queue view.
    public var entries: Int
    public var hasCurrentEntry: Bool
    /// Playback modes, which change what "ended" can mean.
    public var repeatMode: String
    public var shuffle: Bool
    /// Stall inference counter (>= 1 means lilt maps the status to buffering).
    public var stalledSamples: Int
    /// Which transport produced this probe.
    public var mode: String
    /// Number of running helper processes on this machine, including this one.
    /// More than one means two MusicKit clients compete for the system's
    /// playback session, which is the OQ16 hypothesis.
    public var peers: Int
    /// Whether this helper's application is active.
    public var active: Bool
    /// Whether the current entry was observed playing to within a second of its
    /// own duration. A natural end reports "paused" at position 0, exactly like
    /// a pause right after starting, so the position alone cannot classify it.
    public var reachedEnd: Bool

    public init(rawStatus: String, mappedStatus: String, entryID: String, position: Double,
                duration: Double, index: Int, songs: Int, entries: Int, hasCurrentEntry: Bool,
                repeatMode: String, shuffle: Bool, stalledSamples: Int, mode: String,
                peers: Int = 1, active: Bool = false, reachedEnd: Bool = false) {
        self.rawStatus = rawStatus
        self.mappedStatus = mappedStatus
        self.entryID = entryID
        self.position = position
        self.duration = duration
        self.index = index
        self.songs = songs
        self.entries = entries
        self.hasCurrentEntry = hasCurrentEntry
        self.repeatMode = repeatMode
        self.shuffle = shuffle
        self.stalledSamples = stalledSamples
        self.mode = mode
        self.peers = peers
        self.active = active
        self.reachedEnd = reachedEnd
    }
}

/// playbackProbeSignature identifies a state change. Position is excluded: it
/// advances on every sample, and the interesting events are status/entry/queue
/// transitions.
public func playbackProbeSignature(_ probe: PlaybackProbe) -> String {
    "\(probe.mode)|\(probe.rawStatus)|\(probe.mappedStatus)|\(probe.entryID)|\(probe.index)|\(probe.entries)|\(probe.repeatMode)|\(probe.shuffle)|\(probe.hasCurrentEntry)|\(probe.stalledSamples >= 1)|peers\(probe.peers)"
}

/// playbackProbeLine renders one greppable timeline line. Every field is
/// key=value so a session can be diffed and grouped by pid.
public func playbackProbeLine(_ probe: PlaybackProbe, event: String, now: Double, pid: Int32) -> String {
    let signature = playbackProbeSignature(probe)
    return "\(formatSeconds(now)) pid=\(pid) event=\(event) sig=\(signature) "
        + "mode=\(probe.mode) raw=\(probe.rawStatus) mapped=\(probe.mappedStatus) "
        + "pos=\(formatSeconds(probe.position)) dur=\(formatSeconds(probe.duration)) "
        + "entry=\(probe.entryID.isEmpty ? "-" : probe.entryID) current=\(probe.hasCurrentEntry) "
        + "index=\(probe.index) songs=\(probe.songs) entries=\(probe.entries) "
        + "repeat=\(probe.repeatMode) shuffle=\(probe.shuffle) stalled=\(probe.stalledSamples) "
        + "peers=\(probe.peers) active=\(probe.active) reachedEnd=\(probe.reachedEnd)"
}

/// playbackProbeHasEnded reports whether a finite queue played to its end rather
/// than being paused by the user or the system.
///
/// Evidence (2026-09-20, real MusicKit, 322.467s single-song queue): the natural
/// end reports `paused` with the position reset to ~0, and the last observed
/// playing sample was 322.164 — within a second of the duration. A pause right
/// after starting also reports `paused` at position 0, so the classification
/// needs the observed high-water mark, not the paused position.
public func playbackProbeHasEnded(_ probe: PlaybackProbe) -> Bool {
    guard probe.mode == "full", !probe.shuffle, probe.repeatMode == "off" else { return false }
    guard probe.rawStatus == "paused", probe.hasCurrentEntry else { return false }
    return probe.reachedEnd
}

/// reachedEndOfEntry updates the per-entry high-water mark used by
/// playbackProbeHasEnded. It returns the new value: true once the entry has been
/// seen playing within a second of its duration.
public func reachedEndOfEntry(previous: Bool, entryID: String, previousEntryID: String,
                              rawStatus: String, position: Double, duration: Double) -> Bool {
    if entryID != previousEntryID { return false }
    guard duration > 0, rawStatus == "playing" else { return previous }
    return previous || position >= duration - 1
}

/// formatSeconds renders a duration with millisecond precision without
/// Foundation, so the logic target stays dependency-free.
private func formatSeconds(_ value: Double) -> String {
    guard value.isFinite else { return "?" }
    let millis = Int((value * 1000).rounded())
    let sign = millis < 0 ? "-" : ""
    let magnitude = abs(millis)
    let fraction = magnitude % 1000
    let digits = "\(fraction)"
    let padded = String(repeating: "0", count: max(0, 3 - digits.count)) + digits
    return "\(sign)\(magnitude / 1000).\(padded)"
}

/// endedPlaybackStatus maps MusicKit's raw status onto the public one, adding
/// the "ended" state that a finite queue reaches when it plays to its end.
///
/// Evidence (2026-09-20, real MusicKit): a finished queue reports "paused" with
/// the position reset to ~0, so the state cannot be read from the paused
/// snapshot; it comes from the per-entry high-water mark (reachedEndOfEntry).
public func endedPlaybackStatus(rawStatus: String, mappedStatus: String, reachedEnd: Bool,
                                finiteQueue: Bool, shuffle: Bool, repeatMode: String,
                                hasCurrentEntry: Bool) -> String {
    let probe = PlaybackProbe(rawStatus: rawStatus, mappedStatus: mappedStatus, entryID: "e",
                              position: 0, duration: 1, index: 0, songs: 1, entries: 1,
                              hasCurrentEntry: hasCurrentEntry, repeatMode: repeatMode,
                              shuffle: shuffle, stalledSamples: 0, mode: finiteQueue ? "full" : "stream",
                              reachedEnd: reachedEnd)
    if playbackProbeHasEnded(probe) { return "ended" }
    return mappedStatus
}
