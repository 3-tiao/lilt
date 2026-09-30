import XCTest
@testable import LiltPlayerLogic

final class LiltPlayerTests: XCTestCase {
    func testBatchPrepareRefusalHasDedicatedCode() {
        let refusal = NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 6)
        let classified = classifyBatchQueueStartFailure(refusal) as? PlayerError
        XCTAssertEqual(classified?.code, "queue_prepare_rejected")
        XCTAssertEqual(classified?.errorDescription, "MusicKit refused to prepare the finite queue")
    }

    func testBatchPrepareClassificationPreservesOtherErrors() {
        for error in [NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 1),
                      NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 7),
                      NSError(domain: "other", code: 6),
                      NSError(domain: "other", code: 1, userInfo: [NSUnderlyingErrorKey:
                          NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 6)])] {
            let classified = classifyBatchQueueStartFailure(error)
            XCTAssertNil(classified as? PlayerError)
            XCTAssertEqual((classified as NSError).domain, error.domain)
            XCTAssertEqual((classified as NSError).code, error.code)
        }
        XCTAssertTrue(classifyBatchQueueStartFailure(CancellationError()) is CancellationError)
        XCTAssertEqual((classifyBatchQueueStartFailure(PlayerError.authorizationRequired) as? PlayerError)?.code,
                       "authorization_required")
    }

    func testFilledQueueRetriesOneSpecificMusicKitRefusal() async throws {
        let transient = NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 1)
        var attempts = 0
        var waits = 0
        try await startFilledQueue(play: {
            attempts += 1
            if attempts == 1 { throw transient }
        }, wait: { waits += 1 }, isPlaying: { false })
        XCTAssertEqual(attempts, 2)
        XCTAssertEqual(waits, 1)
    }

    func testFilledQueueNormalStartDoesNotWaitOrRetry() async throws {
        var attempts = 0
        var waits = 0
        try await startFilledQueue(play: { attempts += 1 }, wait: { waits += 1 }, isPlaying: { false })
        XCTAssertEqual(attempts, 1)
        XCTAssertEqual(waits, 0)
    }

    func testFilledQueueSecondRefusalIsNotRetried() async {
        let transient = NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 1)
        var attempts = 0
        do {
            try await startFilledQueue(play: { attempts += 1; throw transient }, wait: {}, isPlaying: { false })
            XCTFail("a second refusal must reach the server")
        } catch {
            XCTAssertTrue(isTransientFilledQueueStartRefusal(error))
        }
        XCTAssertEqual(attempts, 2)
    }

    func testFilledQueueDoesNotRetryOtherErrorsOrAfterCancellation() async {
        for error in [NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 2),
                      NSError(domain: "other", code: 1)] {
            var attempts = 0
            do {
                try await startFilledQueue(play: { attempts += 1; throw error }, wait: {}, isPlaying: { false })
                XCTFail("unrelated error must reach the server")
            } catch {
                XCTAssertFalse(isTransientFilledQueueStartRefusal(error))
            }
            XCTAssertEqual(attempts, 1)
        }
        var attempts = 0
        do {
            try await startFilledQueue(play: { attempts += 1; throw NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 1) },
                                       wait: { throw CancellationError() }, isPlaying: { false })
            XCTFail("cancellation must abort before a second play")
        } catch is CancellationError {
            XCTAssertEqual(attempts, 1)
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }

    func testFilledQueueDoesNotStartAgainWhenFirstAttemptActuallyStarted() async throws {
        var attempts = 0
        try await startFilledQueue(play: {
            attempts += 1
            throw NSError(domain: "MPMusicPlayerControllerErrorDomain", code: 1)
        }, wait: {}, isPlaying: { true })
        XCTAssertEqual(attempts, 1)
    }

    func testMusicStartNeedsTheSelectedTrackToAdvance() {
        XCTAssertFalse(musicStartConfirmed(status: "paused", previousPosition: 0, position: 1, currentSongID: "b", expectedSongID: "b"))
        XCTAssertFalse(musicStartConfirmed(status: "playing", previousPosition: nil, position: 1, currentSongID: "b", expectedSongID: "b"))
        XCTAssertFalse(musicStartConfirmed(status: "playing", previousPosition: 0, position: 0, currentSongID: "b", expectedSongID: "b"))
        XCTAssertFalse(musicStartConfirmed(status: "playing", previousPosition: 1, position: 2, currentSongID: "a", expectedSongID: "b"))
        XCTAssertTrue(musicStartConfirmed(status: "playing", previousPosition: 1, position: 2, currentSongID: "b", expectedSongID: "b"))
        XCTAssertTrue(musicStartConfirmed(status: "playing", previousPosition: 0, position: 1, currentSongID: nil, expectedSongID: nil))
        // Shuffle can select any next song. The entry must change, not match
        // the unshuffled row that happened to follow it in the live array.
        XCTAssertFalse(musicStartConfirmed(status: "playing", previousPosition: 1, position: 2,
                                           currentSongID: "a", expectedSongID: nil,
                                           currentEntryID: "old", previousEntryID: "old"))
        XCTAssertTrue(musicStartConfirmed(status: "playing", previousPosition: 0, position: 1,
                                          currentSongID: "c", expectedSongID: nil,
                                          currentEntryID: "new", previousEntryID: "old"))
    }

    func testMusicStartTimeoutIdentifiesWrongSongWithoutLeakingIdentity() {
        var diagnostics = MusicStartDiagnostics()
        diagnostics.observe(status: "playing", previousPosition: nil, position: 0,
                            currentSongID: "private-actual-id", expectedSongID: "private-expected-id",
                            currentEntryID: "private-entry", previousEntryID: nil)
        diagnostics.observe(status: "playing", previousPosition: 0, position: 1,
                            currentSongID: "private-actual-id", expectedSongID: "private-expected-id",
                            currentEntryID: "private-entry", previousEntryID: nil)
        let failure = PlayerError.playbackNotStarted(diagnostics, queueEntries: 39,
                                                      debug: ["expectedID": "private-expected-id", "actualID": "private-actual-id"])
        let detail = failure.errorDescription ?? ""
        XCTAssertEqual(failure.debug?["expectedID"], "private-expected-id")
        XCTAssertEqual(failure.code, "playback_error")
        XCTAssertTrue(detail.contains("playback start was not confirmed"))
        XCTAssertTrue(detail.contains("playing:2,advancing:1,playingAdvance:1,wrongSong:1"))
        XCTAssertTrue(detail.contains("lastStatus:playing,lastSongMatch:no,lastPosition:1.0,queueEntries:39"))
        XCTAssertFalse(detail.contains("private-"))
        XCTAssertFalse(detail.contains("did not start playback"))
    }

    func testMusicStartTimeoutSeparatesStatusProgressAndMissingEntry() {
        var diagnostics = MusicStartDiagnostics()
        diagnostics.observe(status: "paused", previousPosition: 0, position: 2,
                            currentSongID: "secret-song", expectedSongID: "secret-song",
                            currentEntryID: "secret-entry", previousEntryID: "secret-entry")
        diagnostics.observe(status: "playing", previousPosition: 2, position: 2,
                            currentSongID: "secret-song", expectedSongID: "secret-song",
                            currentEntryID: "secret-entry", previousEntryID: "secret-entry")
        diagnostics.observe(status: "playing", previousPosition: 2, position: 3,
                            currentSongID: nil, expectedSongID: "secret-song",
                            currentEntryID: nil, previousEntryID: "secret-entry")
        let detail = diagnostics.summary(queueEntries: 4)
        XCTAssertTrue(detail.contains("samples:3,playing:2,advancing:2,playingAdvance:1"))
        XCTAssertTrue(detail.contains("wrongSong:0,missingSong:1,unchangedEntry:1"))
        XCTAssertTrue(detail.contains("lastStatus:playing,lastSongMatch:missing"))
        XCTAssertFalse(detail.contains("secret-"))
        // An unknown MusicKit status must not become arbitrary text in the log.
        var unknown = MusicStartDiagnostics()
        unknown.observe(status: "unexpected-secret-status", previousPosition: nil, position: 0,
                        currentSongID: nil, expectedSongID: nil,
                        currentEntryID: nil, previousEntryID: nil)
        XCTAssertTrue(unknown.summary(queueEntries: 0).contains("lastStatus:other,lastSongMatch:not_checked"))
        XCTAssertFalse(unknown.summary(queueEntries: 0).contains("secret"))
    }

    func testStableIDWinsOverIndex() {
        let tracks = [StartTrack(id: "a"), StartTrack(id: "b"), StartTrack(id: "c")]
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: "b", index: 2), 1)
    }

    func testStableIDThenIndex() {
        let tracks = [StartTrack(id: "a"), StartTrack(id: "b")]
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: "missing", index: 1), 1)
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: "missing", index: 99), 0)
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: nil, index: nil), 0)
    }

    func testReversedDisplayUsesReversedStableIDIndex() {
        let reversed = [StartTrack(id: "c"), StartTrack(id: "b"), StartTrack(id: "a")]
        XCTAssertEqual(selectedStartIndex(tracks: reversed, id: "a", index: 0), 2)
    }

    func testUnsupportedAndUnavailableEntriesAreFilteredConsistently() {
        let raw = [
            PlaylistEntryDescriptor(kind: "song", id: "a", title: "A", originalIndex: 0),
            PlaylistEntryDescriptor(kind: "music-video", id: "video", title: "Video", originalIndex: 1),
            PlaylistEntryDescriptor(kind: "song", id: "gone", title: "Gone", available: false, originalIndex: 2),
            PlaylistEntryDescriptor(kind: "song", id: "b", title: "B", originalIndex: 3),
        ]
        let displayed = supportedPlaylistEntries(raw)
        XCTAssertEqual(displayed.map(\.id), ["a", "b"])
        XCTAssertEqual(displayed.map(\.originalIndex), [0, 3])
        let starts = displayed.map { StartTrack(id: $0.id) }
        XCTAssertEqual(selectedStartIndex(tracks: starts, id: "b", index: 1), 1)
    }

    func testReverseAppliesAfterSupportedEntryFiltering() {
        let raw = [
            PlaylistEntryDescriptor(kind: "song", id: "a", title: "A", originalIndex: 0),
            PlaylistEntryDescriptor(kind: "music-video", id: "video", title: "Video", originalIndex: 1),
            PlaylistEntryDescriptor(kind: "song", id: "b", title: "B", originalIndex: 2),
        ]
        let displayed = supportedPlaylistEntries(raw, reverse: true)
        XCTAssertEqual(displayed.map(\.id), ["b", "a"])
        XCTAssertEqual(displayed.map(\.originalIndex), [2, 0])
    }

    func testMusicAccountStatusPrioritizesPlaybackThenCloudLibrary() {
        XCTAssertEqual(musicAccountStatus(canPlayCatalogContent: false, hasCloudLibraryEnabled: false), "subscription_required")
        XCTAssertEqual(musicAccountStatus(canPlayCatalogContent: true, hasCloudLibraryEnabled: false), "cloud_library_disabled")
        XCTAssertEqual(musicAccountStatus(canPlayCatalogContent: true, hasCloudLibraryEnabled: true), "ready")
    }

    // The URL-session status contract: a stream that stopped progressing must
    // never read as "paused", or the server's stall watchdog treats the session
    // as resting and the track freezes with no error (observed live on a Jamendo
    // track whose CDN delivered no audio).
    func testMediaSessionStatusNeverReportsAStalledStreamAsPaused() {
        XCTAssertEqual(mediaSessionStatus(mode: "url", ended: false, pauseRequested: false, timeControl: .paused), "buffering")
        XCTAssertEqual(mediaSessionStatus(mode: "stream", ended: false, pauseRequested: false, timeControl: .paused), "buffering")
        XCTAssertEqual(mediaSessionStatus(mode: "url", ended: false, pauseRequested: false, timeControl: .waiting), "buffering")
        XCTAssertEqual(mediaSessionStatus(mode: "url", ended: false, pauseRequested: false, timeControl: .playing), "playing")
    }

    // ...and a pause that was asked for stays paused, whether the client asked
    // (RPC) or a system media key did (which the server never sees as a command).
    func testMediaSessionStatusKeepsARequestedPause() {
        XCTAssertEqual(mediaSessionStatus(mode: "url", ended: false, pauseRequested: true, timeControl: .paused), "paused")
        // The pause has not landed in AVPlayer yet; the requested state wins.
        XCTAssertEqual(mediaSessionStatus(mode: "url", ended: false, pauseRequested: true, timeControl: .playing), "paused")
        XCTAssertEqual(mediaSessionStatus(mode: "none", ended: false, pauseRequested: false, timeControl: .paused), "stopped")
        XCTAssertEqual(mediaSessionStatus(mode: "url", ended: true, pauseRequested: false, timeControl: .playing), "stopped")
    }

    func testProbeErrorCodeClassifiesTransportFailures() {
        XCTAssertEqual(probeErrorCode(domain: "NSURLErrorDomain", code: -1202), "tls")
        XCTAssertEqual(probeErrorCode(domain: "NSURLErrorDomain", code: -1200), "tls")
        XCTAssertEqual(probeErrorCode(domain: "NSURLErrorDomain", code: -1001), "timeout")
        XCTAssertEqual(probeErrorCode(domain: "NSURLErrorDomain", code: -1009), "network")
        XCTAssertEqual(probeErrorCode(domain: "NSURLErrorDomain", code: -1003), "network")
        XCTAssertEqual(probeErrorCode(domain: "AVFoundationErrorDomain", code: -11828), "unsupported")
        XCTAssertEqual(probeErrorCode(domain: "AVFoundationErrorDomain", code: -11800), "unknown")
        XCTAssertEqual(probeErrorCode(domain: "", code: 0), "unknown")
        XCTAssertEqual(probeErrorCode(domain: "SomeOtherDomain", code: 7), "unknown")
    }

    func testProbeHTTPResponseOutcomeClassifiesStatusAndData() {
        XCTAssertEqual(probeHTTPResponseOutcome(statusCode: 200, receivedData: true), .healthy)
        XCTAssertEqual(probeHTTPResponseOutcome(statusCode: 204, receivedData: false), .closedWithoutData)
        XCTAssertEqual(probeHTTPResponseOutcome(statusCode: 302, receivedData: false), .closedWithoutData)
        XCTAssertEqual(probeHTTPResponseOutcome(statusCode: 404, receivedData: true), .httpError)
        XCTAssertEqual(probeHTTPResponseOutcome(statusCode: 503, receivedData: false), .httpError)
    }

    private func song(_ id: String, _ title: String, album: String = "Album", duration: Double = 180) -> QueueSongIdentity {
        QueueSongIdentity(id: id, title: title, artist: "Artist", album: album, duration: duration)
    }

    func testCanonicalQueueIndexResolvesDifferentLibraryAndCatalogIDs() {
        let songs = [song("library-1", "First"), song("library-2", "Second"), song("library-3", "Third")]
        // A natural transition or shuffle can land anywhere; never infer from
        // the previous index or the position in MusicKit's live entries.
        XCTAssertEqual(canonicalQueueIndex(songs: songs, current: song("catalog-3", "Third")), 2)
        XCTAssertEqual(canonicalQueueIndex(songs: songs, current: song("catalog-2", "Second")), 1)
        XCTAssertEqual(canonicalQueueIndex(songs: songs, current: song("library-2", "Different metadata")), 1)
        XCTAssertNil(canonicalQueueIndex(songs: songs, current: nil))
    }

    func testStartTargetAcceptsOnlyUniqueCatalogLibraryIdentity() {
        let submitted = [song("library-1", "Selected"), song("library-2", "Other")]
        let current = song("catalog-1", "Selected")
        XCTAssertTrue(startTargetMatches(songs: submitted, expectedSongID: "library-1", current: current))
        XCTAssertTrue(musicStartConfirmed(status: "playing", previousPosition: 0, position: 1,
                                          currentSongID: current.id, expectedSongID: "library-1",
                                          sameQueueSong: startTargetMatches(songs: submitted, expectedSongID: "library-1", current: current)))
        let wrong = song("catalog-2", "Other")
        XCTAssertFalse(startTargetMatches(songs: submitted, expectedSongID: "library-1", current: wrong))
        XCTAssertFalse(musicStartConfirmed(status: "playing", previousPosition: 0, position: 1,
                                           currentSongID: wrong.id, expectedSongID: "library-1",
                                           sameQueueSong: startTargetMatches(songs: submitted, expectedSongID: "library-1", current: wrong)))
        XCTAssertFalse(startTargetMatches(songs: [song("a", "Same"), song("b", "Same")],
                                          expectedSongID: "a", current: song("catalog", "Same")))
        XCTAssertFalse(startTargetMatches(songs: [song("a", "First"), song("a", "Second")],
                                          expectedSongID: "a", current: song("catalog", "First")))
        XCTAssertFalse(startTargetMatches(songs: submitted, expectedSongID: "library-1", current: nil))
        XCTAssertFalse(startTargetMatches(songs: submitted, expectedSongID: "missing", current: current))
        XCTAssertFalse(startTargetMatches(songs: submitted, expectedSongID: "library-1",
                                          current: song("catalog-1", "Selected", duration: 200)))
        XCTAssertFalse(musicStartConfirmed(status: "paused", previousPosition: 0, position: 1,
                                           currentSongID: current.id, expectedSongID: "library-1", sameQueueSong: true))
        XCTAssertFalse(musicStartConfirmed(status: "playing", previousPosition: 1, position: 1,
                                           currentSongID: current.id, expectedSongID: "library-1", sameQueueSong: true))
    }

    func testCanonicalQueueIndexRefusesAmbiguousOrUnknownSong() {
        let songs = [song("library-1", "Same"), song("library-2", "Same"), song("library-3", "Third")]
        XCTAssertNil(canonicalQueueIndex(songs: songs, current: song("catalog", "Same")))
        XCTAssertNil(canonicalQueueIndex(songs: songs, current: song("catalog", "Not in queue")))
        XCTAssertNil(canonicalQueueIndex(songs: songs, current: song("catalog", "Third", duration: 210)))
        XCTAssertNil(canonicalQueueIndex(songs: songs, current: song("catalog", "Third", album: "Another")))
        XCTAssertNil(canonicalQueueIndex(songs: songs, current: QueueSongIdentity(id: "catalog", title: "Third", artist: "Other", album: "Album", duration: 180)))
        XCTAssertNil(canonicalQueueIndex(songs: songs, current: QueueSongIdentity(id: "catalog", title: "Third", artist: "", album: nil, duration: nil)))
        XCTAssertNil(canonicalQueueIndex(songs: [song("same", "A"), song("same", "A")], current: song("same", "A")))
        XCTAssertNil(canonicalQueueIndex(songs: [], current: song("catalog", "Third")))
    }

    func testCanonicalQueueIndexUsesAlbumAndDurationToDistinguishVersions() {
        let songs = [song("library-1", "Same", album: "First"), song("library-2", "Same", album: "Second")]
        XCTAssertEqual(canonicalQueueIndex(songs: songs, current: song("catalog", "Same", album: "Second")), 1)
        XCTAssertEqual(canonicalQueueIndex(songs: [song("a", "Same", duration: 180), song("b", "Same", duration: 240)],
                                           current: song("catalog", "Same", duration: 241)), 1)
    }

    // Jump/remove/move indices always resolve against the canonical order.
    func testQueueTargetResolvesCanonicalIndex() {
        let ids = ["a", "b", "c"]
        XCTAssertEqual(queueTargetID(ids: ids, index: 1), "b")
        XCTAssertNil(queueTargetID(ids: ids, index: 3))
        XCTAssertNil(queueTargetID(ids: ids, index: -1))
    }

    func testRemoveAndMoveKeepCanonicalIndices() {
        let ids = ["a", "b", "c", "d"]
        XCTAssertEqual(removedQueue(ids, at: 1), ["a", "c", "d"])
        XCTAssertNil(removedQueue(ids, at: 4))
        XCTAssertEqual(movedQueue(ids, from: 0, to: 3), ["b", "c", "d", "a"])
        XCTAssertEqual(movedQueue(ids, from: 3, to: 0), ["d", "a", "b", "c"])
        XCTAssertNil(movedQueue(ids, from: 4, to: 0))
        XCTAssertNil(movedQueue(ids, from: 0, to: 4))
    }

    func testQueueUndoRestoresExactCanonicalPosition() {
        let ids = ["a", "c"]
        XCTAssertEqual(restoredQueue(ids, item: "b", at: 1), ["a", "b", "c"])
        XCTAssertNil(restoredQueue(ids, item: "b", at: 3))
        let post = [song("a", "A"), song("c", "C")]
        XCTAssertTrue(queueUndoContextMatches(postRemove: post, currentQueue: post,
                                              removedIndex: 1, expectedCurrentIndex: 0,
                                              currentIndex: 0))
        XCTAssertFalse(queueUndoContextMatches(postRemove: post, currentQueue: post,
                                               removedIndex: 1, expectedCurrentIndex: 0,
                                               currentIndex: 1))
        XCTAssertFalse(queueUndoContextMatches(postRemove: post,
                                               currentQueue: [song("a", "A"), song("d", "D")],
                                               removedIndex: 1, expectedCurrentIndex: 0,
                                               currentIndex: 0))
    }

    // MusicKit rebuilds entry ids, so live entries are found through their Song
    // payload. The positional entry wins when it already holds the expected
    // song, which keeps duplicate songs on separate rows.
    func testLiveEntryOffsetPrefersPositionThenSongID() {
        let ids: [String?] = ["s1", "s2", "s3"]
        XCTAssertEqual(liveEntryOffset(entrySongIDs: ids, songID: "s2", canonicalIndex: 1), 1)
        XCTAssertEqual(liveEntryOffset(entrySongIDs: ["x", "s2", "s3"], songID: "s1", canonicalIndex: 0), 0)
        XCTAssertEqual(liveEntryOffset(entrySongIDs: ["s3", "s1", "s2"], songID: "s2", canonicalIndex: 2), 2)
    }

    func testLiveEntryOffsetUsesMatchingRowWhenPositionDiffers() {
        XCTAssertEqual(liveEntryOffset(entrySongIDs: ["s2", "s1", "s3"], songID: "s2", canonicalIndex: 1), 0)
    }

    func testLiveEntryOffsetKeepsDuplicateSongsOnDistinctRows() {
        let ids: [String?] = ["same", "other", "same"]
        XCTAssertEqual(liveEntryOffset(entrySongIDs: ids, songID: "same", canonicalIndex: 2), 2)
        XCTAssertEqual(liveEntryOffset(entrySongIDs: ids, songID: "same", canonicalIndex: 0), 0)
    }

    func testLiveEntryOffsetFallsBackToCanonicalPosition() {
        XCTAssertEqual(liveEntryOffset(entrySongIDs: ["a", "b"], songID: "missing", canonicalIndex: 1), 1)
        XCTAssertNil(liveEntryOffset(entrySongIDs: ["a", "b"], songID: "missing", canonicalIndex: 5))
        XCTAssertNil(liveEntryOffset(entrySongIDs: [], songID: "a", canonicalIndex: 0))
    }

    // A live reorder must keep the entry that was playing on the same row so
    // MusicKit does not re-resolve the current entry to the moved one. The
    // calculation deliberately uses a row, not a Song payload ID: duplicates
    // are distinct queue entries.
    func testQueueMoveCurrentRestoreDetectsCrossingThePlayingRow() {
        // Moving an entry that stays below the playing row never moves it.
        XCTAssertNil(queueMoveCurrentRestore(currentIndex: 1, entryCount: 4, from: 3, to: 2))
        // Moving the entry below the playing row above it shifts "b" down one.
        XCTAssertEqual(queueMoveCurrentRestore(currentIndex: 1, entryCount: 4, from: 2, to: 1), 2)
        // Moving the playing entry itself leaves it at its new index.
        XCTAssertEqual(queueMoveCurrentRestore(currentIndex: 1, entryCount: 4, from: 1, to: 2), 2)
        // Duplicate songs are still separate rows and must be restored.
        XCTAssertEqual(queueMoveCurrentRestore(currentIndex: 2, entryCount: 3, from: 2, to: 0), 0)
        XCTAssertNil(queueMoveCurrentRestore(currentIndex: 4, entryCount: 4, from: 1, to: 2))
        XCTAssertNil(queueMoveCurrentRestore(currentIndex: 1, entryCount: 4, from: 9, to: 0))
    }

    func testLiveReorderMatchesTheHelperEdit() {
        let ids = ["a", "b", "c", "d"]
        XCTAssertEqual(liveReorder(ids, from: 0, to: 3), ["b", "c", "d", "a"])
        XCTAssertEqual(liveReorder(ids, from: 3, to: 0), ["d", "a", "b", "c"])
        // `to` beyond the end clamps to the last row after removal.
        XCTAssertEqual(liveReorder(ids, from: 0, to: 9), ["b", "c", "d", "a"])
        XCTAssertNil(liveReorder(ids, from: 4, to: 0))
        XCTAssertNil(liveReorder(ids, from: 0, to: -1))
    }
    // The probe signature marks state changes only: position advances every
    // sample and must not look like a transition.
    func testPlaybackProbeSignatureIgnoresPosition() {
        let base = PlaybackProbe(rawStatus: "playing", mappedStatus: "playing", entryID: "s1",
                                 position: 1, duration: 100, index: 0, songs: 3, entries: 3,
                                 hasCurrentEntry: true, repeatMode: "off", shuffle: false,
                                 stalledSamples: 0, mode: "full")
        var advanced = base
        advanced.position = 9
        XCTAssertEqual(playbackProbeSignature(base), playbackProbeSignature(advanced))

        var paused = base
        paused.rawStatus = "paused"
        paused.mappedStatus = "paused"
        XCTAssertNotEqual(playbackProbeSignature(base), playbackProbeSignature(paused))

        var buffering = base
        buffering.stalledSamples = 1
        XCTAssertNotEqual(playbackProbeSignature(base), playbackProbeSignature(buffering))
    }

    // Every field is key=value so a session log can be grepped and grouped.
    func testPlaybackProbeLineCarriesEveryField() {
        let probe = PlaybackProbe(rawStatus: "paused", mappedStatus: "buffering", entryID: "s1",
                                  position: 3.5, duration: 322.467, index: 1, songs: 12, entries: 12,
                                  hasCurrentEntry: true, repeatMode: "off", shuffle: false,
                                  stalledSamples: 2, mode: "full")
        let line = playbackProbeLine(probe, event: "sample", now: 1758374400.25, pid: 4242)
        for fragment in ["pid=4242", "event=sample", "mode=full", "raw=paused", "mapped=buffering",
                         "pos=3.500", "dur=322.467", "entry=s1", "current=true", "index=1",
                         "songs=12", "entries=12", "repeat=off", "shuffle=false", "stalled=2"] {
            XCTAssertTrue(line.contains(fragment), "missing \(fragment) in \(line)")
        }
        XCTAssertFalse(line.contains("entry= \n"))
    }

    // A natural end is a finite, unshuffled, non-repeating queue whose current
    // entry was observed playing to within a second of its own duration. The
    // paused position is ~0 at the end, so position alone cannot classify it
    // (real session: last playing sample 322.164s of 322.467s, then paused 0.011).
    func testPlaybackProbeEndDetection() {
        func probe(reachedEnd: Bool, repeatMode: String = "off", shuffle: Bool = false,
                   raw: String = "paused", entry: String = "s3", mode: String = "full") -> PlaybackProbe {
            PlaybackProbe(rawStatus: raw, mappedStatus: raw, entryID: entry, position: 0.011,
                          duration: 322.467, index: 2, songs: 3, entries: 3,
                          hasCurrentEntry: !entry.isEmpty, repeatMode: repeatMode, shuffle: shuffle,
                          stalledSamples: 0, mode: mode, reachedEnd: reachedEnd)
        }
        XCTAssertTrue(playbackProbeHasEnded(probe(reachedEnd: true)))
        // Paused without ever reaching the end: a user pause, mid-track or at 0.
        XCTAssertFalse(playbackProbeHasEnded(probe(reachedEnd: false)))
        // Still playing, or already stopped.
        XCTAssertFalse(playbackProbeHasEnded(probe(reachedEnd: true, raw: "playing")))
        // Repeat and shuffle keep a finite queue from ending.
        XCTAssertFalse(playbackProbeHasEnded(probe(reachedEnd: true, repeatMode: "all")))
        XCTAssertFalse(playbackProbeHasEnded(probe(reachedEnd: true, shuffle: true)))
        // No current entry, or a non-MusicKit transport.
        XCTAssertFalse(playbackProbeHasEnded(probe(reachedEnd: true, entry: "")))
        XCTAssertFalse(playbackProbeHasEnded(probe(reachedEnd: true, mode: "preview")))
    }

    // The high-water mark resets per entry and only rises while playing.
    func testReachedEndOfEntryTracksPerEntryHighWaterMark() {
        // Never playing, or a different entry, cannot mark the end.
        XCTAssertFalse(reachedEndOfEntry(previous: false, entryID: "a", previousEntryID: "a",
                                         rawStatus: "paused", position: 322, duration: 322.467))
        XCTAssertFalse(reachedEndOfEntry(previous: false, entryID: "b", previousEntryID: "a",
                                         rawStatus: "playing", position: 322, duration: 322.467))
        // Playing near the end marks it, and the mark sticks for that entry.
        XCTAssertTrue(reachedEndOfEntry(previous: false, entryID: "a", previousEntryID: "a",
                                        rawStatus: "playing", position: 321.5, duration: 322.467))
        XCTAssertTrue(reachedEndOfEntry(previous: true, entryID: "a", previousEntryID: "a",
                                        rawStatus: "paused", position: 0.011, duration: 322.467))
        // Mid-track playing does not.
        XCTAssertFalse(reachedEndOfEntry(previous: false, entryID: "a", previousEntryID: "a",
                                         rawStatus: "playing", position: 100, duration: 322.467))
        // Unknown duration cannot be judged.
        XCTAssertFalse(reachedEndOfEntry(previous: false, entryID: "a", previousEntryID: "a",
                                         rawStatus: "playing", position: 100, duration: 0))
    }


    // The public status adds "ended" for a finite queue that played out, and
    // never for a user pause, a repeated queue, a shuffled queue, or a stream.
    func testEndedPlaybackStatusMapping() {
        func status(reachedEnd: Bool, raw: String = "paused", mapped: String = "paused",
                    finiteQueue: Bool = true, shuffle: Bool = false, repeatMode: String = "off",
                    hasCurrentEntry: Bool = true) -> String {
            endedPlaybackStatus(rawStatus: raw, mappedStatus: mapped, reachedEnd: reachedEnd,
                                finiteQueue: finiteQueue, shuffle: shuffle, repeatMode: repeatMode,
                                hasCurrentEntry: hasCurrentEntry)
        }
        XCTAssertEqual(status(reachedEnd: true), "ended")
        XCTAssertEqual(status(reachedEnd: false), "paused")
        XCTAssertEqual(status(reachedEnd: true, raw: "playing", mapped: "playing"), "playing")
        XCTAssertEqual(status(reachedEnd: true, repeatMode: "all"), "paused")
        XCTAssertEqual(status(reachedEnd: true, shuffle: true), "paused")
        XCTAssertEqual(status(reachedEnd: true, hasCurrentEntry: false), "paused")
        XCTAssertEqual(status(reachedEnd: true, finiteQueue: false), "paused")
        // A stall is still reported as buffering, not as an end.
        XCTAssertEqual(status(reachedEnd: false, raw: "playing", mapped: "buffering"), "buffering")
    }

    // PlayerError's code is the wire error code the Go server maps; the
    // queueNotJumpable refusal must stay distinct from a generic playback failure
    // and keep its actionable copy (batch 2026-09-23-polish P1).
    func testPlayerErrorCodeAndCopy() {
        XCTAssertEqual(PlayerError.queueNotJumpable(keptPlaying: true).code, "queue_not_jumpable")
        XCTAssertEqual(PlayerError.queueNotJumpable(keptPlaying: false).code, "queue_not_jumpable")
        let kept = PlayerError.queueNotJumpable(keptPlaying: true).errorDescription ?? ""
        XCTAssertTrue(kept.contains("cannot be jumped"), "kept-playing copy: \(kept)")
        XCTAssertTrue(kept.contains("playback continues"), "kept-playing copy: \(kept)")
        let stopped = PlayerError.queueNotJumpable(keptPlaying: false).errorDescription ?? ""
        XCTAssertTrue(stopped.contains("press p"), "stopped copy: \(stopped)")
        // Queue edits refused in preview mode keep the previewUnsupported code and
        // a copy that names queue edits (P2: the helper must not silently no-op).
        XCTAssertEqual(PlayerError.previewUnsupported.code, "preview_unsupported")
        let preview = PlayerError.previewUnsupported.errorDescription ?? ""
        XCTAssertTrue(preview.contains("queue edits"), "preview copy: \(preview)")
    }
}
