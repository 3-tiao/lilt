import XCTest
@testable import LiltPlayerLogic

final class LiltPlayerTests: XCTestCase {
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

    func testNaturalEndOnlyAppliesToItsSession() {
        XCTAssertTrue(urlEndedApplies(activeGeneration: 2, activeSession: "current", callbackGeneration: 2, callbackSession: "current"))
        XCTAssertFalse(urlEndedApplies(activeGeneration: 2, activeSession: "current", callbackGeneration: 1, callbackSession: "current"))
        XCTAssertFalse(urlEndedApplies(activeGeneration: 2, activeSession: "current", callbackGeneration: 2, callbackSession: "old"))
    }

    // Shuffle reorders MusicKit's live entries, so the wire state must project
    // the canonical submitted order and locate the current song by identity.
    func testCanonicalQueueReportsSubmittedOrderWithCurrentByID() {
        let ids = ["a", "b", "c", "d"]
        XCTAssertEqual(canonicalQueue(ids: ids, currentID: "c").queue, ids)
        XCTAssertEqual(canonicalQueue(ids: ids, currentID: "c").index, 2)
        XCTAssertEqual(canonicalQueue(ids: ids, currentID: nil).index, 0)
        XCTAssertEqual(canonicalQueue(ids: ids, currentID: "gone").index, 0)
        XCTAssertEqual(canonicalQueue(ids: [], currentID: "a").queue, [])
    }

    func testCanonicalQueueIndexPrefersCurrentSongThenFallsBack() {
        let ids = ["a", "b", "c", "d"]
        XCTAssertEqual(canonicalQueueIndex(ids: ids, currentSongID: "b", fallbackIndex: 0), 1)
        XCTAssertEqual(canonicalQueueIndex(ids: ids, currentSongID: "c", fallbackIndex: 3), 2)
        XCTAssertEqual(canonicalQueueIndex(ids: ids, currentSongID: nil, fallbackIndex: 3), 3)
        XCTAssertEqual(canonicalQueueIndex(ids: ids, currentSongID: "gone", fallbackIndex: 99), 3)
        XCTAssertEqual(canonicalQueueIndex(ids: [], currentSongID: "a", fallbackIndex: 0), 0)
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
}
