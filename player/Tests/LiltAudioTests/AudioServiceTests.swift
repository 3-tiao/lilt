import AppKit
import LiltPlayerLogic
import XCTest
@testable import LiltHelperKit

@MainActor private final class FakeAudioPlayback: AudioPlayback {
    var position = 0.0
    var timeControl: MediaTimeControl = .playing
    var failure: String?
    var pauses = 0
    var onTime: (@MainActor () -> Void)?
    var onEnd: (@MainActor () -> Void)?
    var onFailure: (@MainActor (String) -> Void)?
    func play() { timeControl = .playing }
    func pause() { pauses += 1; timeControl = .paused }
    func observeTime(_ callback: @escaping @MainActor () -> Void) { onTime = callback }
    func observeEnd(_ callback: @escaping @MainActor () -> Void) { onEnd = callback }
    func observeFailure(_ callback: @escaping @MainActor (String) -> Void) { onFailure = callback }
    func invalidateObservers() { onTime = nil; onEnd = nil; onFailure = nil }
}

final class AudioServiceTests: XCTestCase {
    @MainActor private func play(_ service: AudioService, id: String, artwork: Bool = false) async throws {
        let params: [String: Any] = [
            "url": "https://media.invalid/\(id)", "title": id, "providerID": id,
            "playbackGeneration": 7, "transportSessionID": "same-queue",
        ].merging(artwork ? ["artworkURL": "https://artwork.invalid/\(id)"] : [:]) { _, new in new }
        let data = try JSONSerialization.data(withJSONObject: ["jsonrpc": "2.0", "id": 1, "method": "urlPlay", "params": params])
        let request = try JSONDecoder().decode(RPCRequest.self, from: data)
        let response = await service.handle(request)
        XCTAssertNil(response.0.error)
    }

    @MainActor func testLateCallbacksCannotAffectNextItemInSameSession() async throws {
        for (kind, nextID) in [("failure", "B"), ("end", "B"), ("time", "B"),
                               ("failure", "A"), ("end", "A"), ("time", "A")] {
            var players: [FakeAudioPlayback] = []
            var presentations = 0
            let service = AudioService(makePlayback: { _ in
                let player = FakeAudioPlayback(); players.append(player); return player
            }, fetchArtwork: { _ in XCTFail("unexpected artwork request"); return nil },
            presentNowPlaying: { _, _ in presentations += 1 })
            try await play(service, id: "A")
            let first = players[0]
            // Saving the closure models delivery already queued before the
            // old observers are invalidated. Removing observers cannot undo it.
            let failure = first.onFailure!
            let end = first.onEnd!
            let time = first.onTime!
            try await play(service, id: nextID)
            let before = presentations
            switch kind {
            case "failure": failure("late A failure")
            case "end": end()
            default: time()
            }
            let state = service.state()
            XCTAssertEqual(state.track?.id, nextID, kind)
            XCTAssertEqual(state.status, "playing", kind)
            XCTAssertNil(state.playbackError, kind)
            XCTAssertNil(state.ended, kind)
            XCTAssertEqual(players[1].pauses, 0, kind)
            XCTAssertEqual(presentations, before, kind)
            service.stop()
            let afterStop = presentations
            failure("failure after stop"); end(); time()
            XCTAssertEqual(service.state().status, "stopped")
            XCTAssertNil(service.state().track)
            XCTAssertEqual(presentations, afterStop, "stopped callbacks must not publish")
        }
    }

    @MainActor func testLateArtworkCannotDecorateNextItemInSameSession() async throws {
        let entered = expectation(description: "A artwork suspended")
        var continuation: CheckedContinuation<NSImage?, Never>?
        var presentations: [(State?, NSImage?)] = []
        var players: [FakeAudioPlayback] = []
        let service = AudioService(makePlayback: { _ in
            let player = FakeAudioPlayback(); players.append(player); return player
        }, fetchArtwork: { _ in
            await withCheckedContinuation { continuation = $0; entered.fulfill() }
        }, presentNowPlaying: { presentations.append(($0, $1)) })
        try await play(service, id: "A", artwork: true)
        let task = service.artworkTask
        await fulfillment(of: [entered], timeout: 1)
        try await play(service, id: "B")
        let before = presentations.count
        continuation?.resume(returning: NSImage(size: NSSize(width: 1, height: 1)))
        await task?.value
        XCTAssertEqual(service.state().track?.id, "B")
        XCTAssertEqual(presentations.count, before, "A's image must not be published for B")
        players[1].onTime?()
        XCTAssertNil(presentations.last?.1, "B must not keep A's image")
        service.stop()
    }

    @MainActor func testCurrentArtworkAppliesAndStoppedArtworkDoesNot() async throws {
        let image = NSImage(size: NSSize(width: 1, height: 1))
        var presentations: [(State?, NSImage?)] = []
        let service = AudioService(makePlayback: { _ in FakeAudioPlayback() }, fetchArtwork: { _ in image },
                                   presentNowPlaying: { presentations.append(($0, $1)) })
        try await play(service, id: "A", artwork: true)
        await service.artworkTask?.value
        XCTAssertEqual(presentations.last?.0?.track?.id, "A")
        XCTAssertTrue(presentations.last?.1 === image)
        service.stop()

        let entered = expectation(description: "artwork pending before stop")
        var continuation: CheckedContinuation<NSImage?, Never>?
        let stopped = AudioService(makePlayback: { _ in FakeAudioPlayback() }, fetchArtwork: { _ in
            await withCheckedContinuation { continuation = $0; entered.fulfill() }
        }, presentNowPlaying: { presentations.append(($0, $1)) })
        try await play(stopped, id: "B", artwork: true)
        let task = stopped.artworkTask
        await fulfillment(of: [entered], timeout: 1)
        stopped.stop()
        let before = presentations.count
        continuation?.resume(returning: image)
        await task?.value
        XCTAssertEqual(stopped.state().status, "stopped")
        XCTAssertEqual(presentations.count, before, "stopped artwork must not publish")
        XCTAssertNil(presentations.last?.0)
        XCTAssertNil(presentations.last?.1)
    }

    @MainActor func testCurrentItemCallbacksStillApply() async throws {
        var players: [FakeAudioPlayback] = []
        let service = AudioService(makePlayback: { _ in
            let player = FakeAudioPlayback(); players.append(player); return player
        }, fetchArtwork: { _ in nil }, presentNowPlaying: { _, _ in })
        try await play(service, id: "A")
        players[0].onFailure?("current failure")
        XCTAssertEqual(service.state().status, "error")
        XCTAssertEqual(service.state().playbackError, "current failure")
        XCTAssertEqual(players[0].pauses, 1)
        players[0].onFailure?("duplicate failure")
        XCTAssertEqual(players[0].pauses, 1)
        try await play(service, id: "B")
        players[1].onEnd?()
        XCTAssertEqual(service.state().status, "stopped")
        XCTAssertEqual(service.state().ended, true)
        XCTAssertEqual(players[1].pauses, 1)
        try await play(service, id: "C")
        players[2].failure = "current polling failure"
        players[2].onTime?()
        XCTAssertEqual(service.state().playbackError, "current polling failure")
        XCTAssertEqual(players[2].pauses, 1)
        service.stop()
    }
}
