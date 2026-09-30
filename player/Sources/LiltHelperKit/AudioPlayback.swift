import AVFoundation
import Foundation
import LiltPlayerLogic

// AudioService owns playback policy; this boundary only observes and controls
// one media instance. Tests inject a player without AVFoundation or system I/O.
@MainActor protocol AudioPlayback: AnyObject {
    var position: Double { get }
    var timeControl: MediaTimeControl { get }
    var failure: String? { get }
    func play()
    func pause()
    func observeTime(_ callback: @escaping @MainActor () -> Void)
    func observeEnd(_ callback: @escaping @MainActor () -> Void)
    func observeFailure(_ callback: @escaping @MainActor (String) -> Void)
    func invalidateObservers()
}

@MainActor final class AVFoundationAudioPlayback: AudioPlayback {
    private let player: AVPlayer
    private var timeObserver: Any?
    private var endObserver: NSObjectProtocol?
    private var failureObserver: NSObjectProtocol?
    private var itemObservation: NSKeyValueObservation?

    init(url: URL, volume: Float) {
        player = AVPlayer(url: url)
        player.volume = volume
    }

    var position: Double {
        let value = player.currentTime().seconds
        return value.isFinite ? value : 0
    }
    var timeControl: MediaTimeControl {
        switch player.timeControlStatus {
        case .playing: return .playing
        case .waitingToPlayAtSpecifiedRate: return .waiting
        default: return .paused
        }
    }
    var failure: String? {
        guard let item = player.currentItem, item.status == .failed else { return nil }
        return item.error?.localizedDescription ?? "the stream could not be loaded"
    }
    func play() { player.play() }
    func pause() { player.pause() }

    func observeTime(_ callback: @escaping @MainActor () -> Void) {
        timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(seconds: 1, preferredTimescale: 10), queue: .main) { _ in
            MainActor.assumeIsolated { callback() }
        }
    }
    func observeEnd(_ callback: @escaping @MainActor () -> Void) {
        endObserver = NotificationCenter.default.addObserver(forName: .AVPlayerItemDidPlayToEndTime, object: player.currentItem, queue: .main) { _ in
            MainActor.assumeIsolated { callback() }
        }
    }
    func observeFailure(_ callback: @escaping @MainActor (String) -> Void) {
        failureObserver = NotificationCenter.default.addObserver(forName: .AVPlayerItemFailedToPlayToEndTime, object: player.currentItem, queue: .main) { note in
            let failure = note.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? Error
            let message = failure?.localizedDescription ?? "playback failed before the end of the stream"
            MainActor.assumeIsolated { callback(message) }
        }
        itemObservation = player.currentItem?.observe(\.status, options: [.new, .initial]) { item, _ in
            guard item.status == .failed else { return }
            let message = item.error?.localizedDescription ?? "the stream could not be loaded"
            Task { @MainActor in callback(message) }
        }
    }
    func invalidateObservers() {
        if let timeObserver { player.removeTimeObserver(timeObserver) }
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        if let failureObserver { NotificationCenter.default.removeObserver(failureObserver) }
        itemObservation?.invalidate()
        timeObserver = nil
        endObserver = nil
        failureObserver = nil
        itemObservation = nil
    }
}
