import Foundation

// PlayerError is the helper's typed failure vocabulary: its `code` is what the
// Go server sees as the RPC error code, and `errorDescription` is the copy the
// server keeps as the user-facing message for codes it maps one-to-one (see
// server mapHelperCode). queueNotJumpable keeps an append-queue jump refusal
// distinct from a generic playback start failure and carries the actionable
// way out (batch 2026-09-23-polish P1).
public enum PlayerError: LocalizedError {
    case invalidReference, invalidSearch, previewUnavailable, previewSearchUnavailable, previewUnsupported, authorizationRequired, queueUnavailable, nothingPlaying, unknownMethod, pauseNotApplied
    case playbackNotStarted(MusicStartDiagnostics, queueEntries: Int, debug: [String: String])
    case queueNotJumpable(keptPlaying: Bool)
    case queueUndoUnavailable

    public var code: String {
        switch self {
        case .previewUnavailable: return "preview_unavailable"
        case .previewSearchUnavailable: return "preview_search_unavailable"
        case .previewUnsupported: return "preview_unsupported"
        case .authorizationRequired: return "authorization_required"
        case .queueUnavailable: return "queue_unavailable"
        case .nothingPlaying: return "nothing_playing"
        case .invalidReference: return "invalid_reference"
        case .invalidSearch: return "invalid_search"
        case .unknownMethod: return "unknown_command"
        case .playbackNotStarted, .pauseNotApplied: return "playback_error"
        case .queueNotJumpable: return "queue_not_jumpable"
        case .queueUndoUnavailable: return "queue_undo_unavailable"
        }
    }

    // Deliberately separate from errorDescription: identifiers and song names
    // may only reach the private local debug journal, never public error copy.
    public var debug: [String: String]? {
        if case .playbackNotStarted(_, _, let fields) = self { return fields }
        return nil
    }

    public var errorDescription: String? {
        switch self {
        case .invalidReference: return "play requires a catalog song reference"
        case .invalidSearch: return "search requires a non-empty term"
        case .previewUnavailable: return "this catalog song has no usable preview asset"
        case .previewSearchUnavailable: return "Apple preview search is unavailable"
        case .previewUnsupported: return "next, previous, and queue edits are unavailable in preview mode"
        case .authorizationRequired: return "Apple Music authorization is required"
        case .queueUnavailable: return "nothing is playing yet; start playback before queueing"
        case .nothingPlaying: return "nothing is playing to resume"
        case .unknownMethod: return "unknown JSON-RPC method"
        case .playbackNotStarted(let diagnostics, let queueEntries, _):
            return "MusicKit playback start was not confirmed; playback was stopped rather than reporting success; \(diagnostics.summary(queueEntries: queueEntries))"
        case .pauseNotApplied: return "MusicKit did not confirm pause; playback was stopped rather than reporting success"
        case .queueNotJumpable(let keptPlaying):
            return keptPlaying
                ? "this queue was built track by track and cannot be jumped; playback continues — start the row from its list instead"
                : "this queue was built track by track and cannot be jumped; playback stopped — press p to start it again"
        case .queueUndoUnavailable: return "the removed song can no longer be restored exactly"
        }
    }
}
