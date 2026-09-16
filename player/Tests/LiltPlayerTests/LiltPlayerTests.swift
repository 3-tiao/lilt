import XCTest
@testable import LiltPlayerLogic

final class LiltPlayerTests: XCTestCase {
    func testStableIDWinsOverIndexAndTitle() {
        let tracks = [StartTrack(id: "a", title: "Same"), StartTrack(id: "b", title: "Same"), StartTrack(id: "c", title: "Last")]
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: "b", index: 2, title: "Same"), 1)
    }

    func testIndexThenCompatibilityTitleFallback() {
        let tracks = [StartTrack(id: "a", title: "First"), StartTrack(id: "b", title: "Second")]
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: "missing", index: 1, title: "First"), 1)
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: "missing", index: 99, title: "Second"), 1)
        XCTAssertEqual(selectedStartIndex(tracks: tracks, id: nil, index: nil, title: "missing"), 0)
    }

    func testReversedDisplayUsesReversedStableIDIndex() {
        let reversed = [StartTrack(id: "c", title: "C"), StartTrack(id: "b", title: "B"), StartTrack(id: "a", title: "A")]
        XCTAssertEqual(selectedStartIndex(tracks: reversed, id: "a", index: 0, title: "A"), 2)
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
        let starts = displayed.map { StartTrack(id: $0.id, title: $0.title) }
        XCTAssertEqual(selectedStartIndex(tracks: starts, id: "b", index: 1, title: "B"), 1)
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
}
