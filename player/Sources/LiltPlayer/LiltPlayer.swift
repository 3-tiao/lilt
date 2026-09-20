import AppKit
import AVFoundation
import Combine
import Darwin
import Foundation
import MusicKit
import LiltHelperKit
import LiltPlayerLogic

typealias Track = LiltHelperKit.HelperTrack

struct PlaybackRequest: Codable { let kind: String; let id: String?; let storefront: String?; let url: String?; let startAt: Int?; let startTrackID: String?; let reverse: Bool?; let fromHere: Bool? }
struct Authorization: Codable {
    let status: String
    let accountStatus: String?
    let accountError: String?
    let countryCode: String?
    let canPlayCatalogContent: Bool
    let hasCloudLibraryEnabled: Bool
}
struct TokenDiagnostics: Codable {
    let authorization: String
    let bundleID: String?
    let developerTokenReceived: Bool
    let developerTokenError: String?
    let kid: String?
    let issuer: String?
    let issuedAt: Int?
    let expiresAt: Int?
    let userTokenReceived: Bool
    let userTokenError: String?
    let subscriptionReceived: Bool
    let subscriptionError: String?
    let canPlayCatalogContent: Bool
    let hasCloudLibraryEnabled: Bool
    let countryCode: String?
    let countryCodeError: String?
    let libraryPlaylistCount: Int?
    let libraryPlaylistError: String?
    let storefrontUSStatus: Int?
    let storefrontCNStatus: Int?
}
struct ITunesSearchResponse: Decodable { let results: [ITunesSong] }
struct ITunesSong: Decodable { let trackId: Int; let trackName: String; let artistName: String; let trackViewUrl: String?; let previewUrl: String? }
enum Result: Encodable {
    case state(State), stateSnapshot(StateSnapshot), authorization(Authorization), diagnostics(TokenDiagnostics), hello(Hello), tracks([Track]), albumTracks(Track, [Track]), playlistTracks(Track, [Track]), empty
    func encode(to encoder: Encoder) throws {
        switch self {
        case .state(let value): try value.encode(to: encoder)
        case .stateSnapshot(let value): try value.encode(to: encoder)
        case .authorization(let value): try value.encode(to: encoder)
        case .diagnostics(let value): try value.encode(to: encoder)
        case .hello(let value): try value.encode(to: encoder)
        case .tracks(let value): try value.encode(to: encoder)
        case .albumTracks(let album, let items):
            var c = encoder.singleValueContainer()
            try c.encode(AlbumTracksPayload(album: album, items: items))
        case .playlistTracks(let playlist, let items):
            var c = encoder.singleValueContainer()
            try c.encode(PlaylistTracksPayload(playlist: playlist, items: items))
        case .empty: var c = encoder.singleValueContainer(); try c.encode([String: String]())
        }
    }
}
struct AlbumTracksPayload: Encodable { let album: Track; let items: [Track] }
struct PlaylistTracksPayload: Encodable { let playlist: Track; let items: [Track] }
struct RPCResponse: Encodable { let jsonrpc = "2.0"; let id: Int; let result: Result?; let error: RPCError? }
struct RPCNotification: Encodable { let jsonrpc = "2.0"; let method = "stateChanged"; let params: StateSnapshot }

final class RPCSocketServer: @unchecked Sendable {
    private let path: String
    private let lock = NSLock()
    private let writerQueue = DispatchQueue(label: "com.caiguo.lilt-player.rpc-writer")
    private var listener: Int32 = -1
    private var connection: Int32 = -1
    private var stopped = false
    private var hostConnected = false
    private var watchdog: DispatchWorkItem?
    private var subscribed = false
    private var sequence: UInt64 = 0
    private var lastStateData: Data?

    init(path: String) { self.path = path }

    func start() throws {
        guard path.utf8.count < MemoryLayout.size(ofValue: sockaddr_un().sun_path) else { throw SocketError.pathTooLong }
        guard !FileManager.default.fileExists(atPath: path) else { throw SocketError.pathExists }
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw SocketError.system("socket", errno) }

        var address = sockaddr_un()
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        address.sun_family = sa_family_t(AF_UNIX)
        let pathCapacity = MemoryLayout.size(ofValue: address.sun_path)
        path.withCString { source in
            withUnsafeMutablePointer(to: &address.sun_path) { tuple in
                tuple.withMemoryRebound(to: CChar.self, capacity: pathCapacity) { destination in
                    _ = strncpy(destination, source, pathCapacity - 1)
                }
            }
        }
        let bindResult = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard bindResult == 0 else { let code = errno; Darwin.close(fd); throw SocketError.system("bind", code) }
        guard Darwin.chmod(path, mode_t(S_IRUSR | S_IWUSR)) == 0 else {
            let code = errno; Darwin.close(fd); Darwin.unlink(path); throw SocketError.system("chmod", code)
        }
        guard Darwin.listen(fd, 1) == 0 else {
            let code = errno; Darwin.close(fd); Darwin.unlink(path); throw SocketError.system("listen", code)
        }
        lock.lock(); listener = fd; lock.unlock()

        let watchdog = DispatchWorkItem { [weak self] in
            guard let self else { return }
            self.lock.lock(); let abandoned = !self.hostConnected && !self.stopped; self.lock.unlock()
            if abandoned { DispatchQueue.main.async { NSApplication.shared.terminate(nil) } }
        }
        self.watchdog = watchdog
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in self?.acceptHost() }
        // Go waits 20 seconds. Exit first if no host arrives so startup failure
        // cannot leave an app instance behind after the host gives up.
        DispatchQueue.global().asyncAfter(deadline: .now() + 15, execute: watchdog)
    }

    private func acceptHost() {
        lock.lock(); let fd = listener; lock.unlock()
        let accepted = Darwin.accept(fd, nil, nil)
        guard accepted >= 0 else { return }
        var noSigPipe: Int32 = 1
        _ = setsockopt(accepted, SOL_SOCKET, SO_NOSIGPIPE, &noSigPipe, socklen_t(MemoryLayout<Int32>.size))
        lock.lock()
        if stopped { lock.unlock(); Darwin.close(accepted); return }
        connection = accepted
        hostConnected = true
        watchdog?.cancel()
        lock.unlock()
        Task { await process(accepted) }
    }

    private func process(_ fd: Int32) async {
        let file = FileHandle(fileDescriptor: fd, closeOnDealloc: false)
        let decoder = JSONDecoder()
        do {
            for try await line in file.bytes.lines {
                guard let request = try? decoder.decode(RPCRequest.self, from: Data(line.utf8)), request.jsonrpc == "2.0" else { continue }
                let response: RPCResponse
                let shouldShutdown: Bool
                if request.method == "subscribeState" {
                    let snapshot = await LiltPlayer.state()
                    response = RPCResponse(id: request.id, result: .stateSnapshot(subscribe(to: snapshot)), error: nil)
                    shouldShutdown = false
                } else if request.method == "unsubscribeState" {
                    unsubscribe()
                    response = RPCResponse(id: request.id, result: .empty, error: nil)
                    shouldShutdown = false
                } else {
                    (response, shouldShutdown) = await LiltPlayer.handle(request)
                }
                try await send(response)
                if response.error == nil && LiltPlayer.isStateChanging(request.method) {
                    publish(await LiltPlayer.state())
                }
                if shouldShutdown { break }
            }
        } catch {
            fputs("lilt-player RPC connection ended: \(error.localizedDescription)\n", stderr)
        }
        stop()
        await LiltPlayer.stopPlayback()
        await MainActor.run { NSApplication.shared.terminate(nil) }
    }

    private func encoded<T: Encodable>(_ value: T) throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        var data = try encoder.encode(value)
        data.append(0x0A)
        return data
    }

    private func send<T: Encodable>(_ value: T) async throws {
        let data = try encoded(value)
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            writerQueue.async { [weak self] in
                guard let self else {
                    continuation.resume(throwing: CocoaError(.fileWriteUnknown))
                    return
                }
                do {
                    try self.write(data)
                    continuation.resume()
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }

    private func write(_ data: Data) throws {
        lock.lock(); let fd = stopped ? -1 : connection; lock.unlock()
        guard fd >= 0 else { throw CocoaError(.fileNoSuchFile) }
        try data.withUnsafeBytes { rawBuffer in
            guard let base = rawBuffer.baseAddress else { return }
            var offset = 0
            while offset < rawBuffer.count {
                let count = Darwin.write(fd, base.advanced(by: offset), rawBuffer.count - offset)
                if count < 0 {
                    if errno == EINTR { continue }
                    throw SocketError.system("write", errno)
                }
                guard count > 0 else { throw CocoaError(.fileWriteUnknown) }
                offset += count
            }
        }
    }

    private func stateData(_ state: State) -> Data? {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return try? encoder.encode(state)
    }

    private func subscribe(to state: State) -> StateSnapshot {
        lock.lock()
        subscribed = true
        lastStateData = stateData(state)
        let snapshot = StateSnapshot(sequence: sequence, state: state, playbackGeneration: state.playbackGeneration, transportSessionID: state.transportSessionID)
        lock.unlock()
        return snapshot
    }

    private func unsubscribe() {
        lock.lock(); subscribed = false; lock.unlock()
    }

    func publish(_ state: State) {
        lock.lock()
        let data = stateData(state)
        guard subscribed, data != lastStateData else { lock.unlock(); return }
        sequence &+= 1
        lastStateData = data
        let notification = RPCNotification(params: StateSnapshot(sequence: sequence, state: state, playbackGeneration: state.playbackGeneration, transportSessionID: state.transportSessionID))
        guard let notificationData = try? encoded(notification) else { lock.unlock(); return }
        // Submission occurs while holding the state lock, so notification
        // sequence order and writer queue order cannot diverge.
        writerQueue.async { [weak self] in try? self?.write(notificationData) }
        lock.unlock()
    }

    func stop() {
        lock.lock()
        if stopped { lock.unlock(); return }
        stopped = true
        let listenerFD = listener
        let connectionFD = connection
        listener = -1
        connection = -1
        watchdog?.cancel()
        lock.unlock()
        if connectionFD >= 0 {
            writerQueue.sync {
                Darwin.shutdown(connectionFD, SHUT_RDWR)
                Darwin.close(connectionFD)
            }
        }
        if listenerFD >= 0 { Darwin.shutdown(listenerFD, SHUT_RDWR); Darwin.close(listenerFD) }
        Darwin.unlink(path)
    }
}

@main
@MainActor
final class LiltPlayer: NSObject, NSApplicationDelegate {
    private static var retainedDelegate: LiltPlayer?
    private static var previewPlayer: AVPlayer?
    private static var currentTrack: Track?
    // The resolved songs backing the MusicKit queue, kept in the same order as
    // the live queue. queueJump rebuilds from these (fresh Queue.Entry values,
    // as the initial play does) instead of reusing the live entries, which
    // MusicKit rejects with "unexpected start item". Nil once a queue edit makes
    // the mapping unknowable (for example inserting a whole playlist).
    private static var queueSongs: [Song]?
    // Queue positions are canonical (submitted song order); MusicKit's live
    // entry ids are not (it rebuilds them whenever a queue is assigned or
    // advances), so live entries are located through their Song payload id.
    private static var mode = "none"
    private static var queueCursor = 0
    // preferQueueWalk marks a queue that MusicKit will not rebuild: queues it
    // built by appending entries one at a time (playSongs, album playback) and
    // queues whose rebuild it already rejected. The flag blocks the silent
    // skipToNextEntry fallback, which lands past the chosen row because MusicKit
    // skips entries it cannot prepare (batch 2026-09-20-search-and-queue N6).
    private static var preferQueueWalk = false
    private static var variantCache: [String: [String]] = [:]
    private static var variantInFlight: Set<String> = []
    private static var recentlyPlayedCloudUnavailable = false
    private static weak var statePublisher: RPCSocketServer?
    private static var musicStateObserver: AnyCancellable?
    private static var progressSampler: DispatchSourceTimer?
    // MusicKit keeps playbackStatus == .playing while audio is stalled, so the
    // sampler infers buffering from a position that stops advancing.
    private static var stalledSamples = 0
    private static var lastSampledPosition: Double?
    private static var lastSampledAt: Date?
    private static var observedAVPlayer: AVPlayer?
    private static var avTimeObserver: Any?
    private static var avStatusObserver: NSKeyValueObservation?
    private static var avItemStatusObserver: NSKeyValueObservation?
    private static var avFailureObserver: NSObjectProtocol?
    private static var playbackError: String?
    private static var accountStatus: String?
    private static var accountError: String?
    private static var accountCountryCode: String?
    private static var accountCanPlayCatalogContent = false
    private static var accountHasCloudLibraryEnabled = false
    private static var accountRefreshTask: Task<Void, Never>?
    private static var accountRefreshSamples = 0
    private var server: RPCSocketServer?
    private var authorizationOnly = false
    private var signalSources: [DispatchSourceSignal] = []

    static func main() {
        let arguments = CommandLine.arguments
        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)
        let delegate = LiltPlayer()
        retainedDelegate = delegate
        app.delegate = delegate

        if arguments.contains("--authorize") {
            delegate.authorizationOnly = true
        } else if let index = arguments.firstIndex(of: "--rpc-socket"), arguments.indices.contains(index + 1) {
            let server = RPCSocketServer(path: arguments[index + 1])
            delegate.server = server
            connectStatePublisher(server)
            do { try server.start() }
            catch {
                fputs("lilt-player could not start RPC socket: \(error.localizedDescription)\n", stderr)
                exit(EXIT_FAILURE)
            }
        } else {
            fputs("usage: lilt-player --authorize | --rpc-socket <path>\n", stderr)
            exit(EXIT_FAILURE)
        }
        delegate.installSignalHandlers()
        app.run()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        if authorizationOnly {
            NSApplication.shared.activate(ignoringOtherApps: true)
            Task { _ = await MusicAuthorization.request(); NSApplication.shared.terminate(nil) }
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        Self.accountRefreshTask?.cancel()
        server?.stop()
        Self.stopPlayback()
    }

    private func installSignalHandlers() {
        for signalNumber in [SIGINT, SIGTERM] {
            Darwin.signal(signalNumber, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: signalNumber, queue: .main)
            source.setEventHandler { NSApplication.shared.terminate(nil) }
            source.resume()
            signalSources.append(source)
        }
    }

    static func handle(_ request: RPCRequest) async -> (RPCResponse, Bool) {
        do {
            let result = try await dispatch(request)
            return (RPCResponse(id: request.id, result: result, error: nil), request.method == "shutdown")
        } catch let error as PlayerError {
            return (RPCResponse(id: request.id, result: nil, error: RPCError(code: error.code, message: error.localizedDescription)), false)
        } catch {
            return (RPCResponse(id: request.id, result: nil, error: RPCError(code: "music_error", message: errorDetails(error))), false)
        }
    }

    nonisolated static func isStateChanging(_ method: String) -> Bool {
        ["play", "pause", "resume", "next", "previous", "setShuffle", "setRepeat", "stop", "enqueue", "playSongs", "queueJump", "queueRemove", "queueMove", "queueClear"].contains(method)
    }

    static func connectStatePublisher(_ publisher: RPCSocketServer) {
        statePublisher = publisher
        musicStateObserver = ApplicationMusicPlayer.shared.state.objectWillChange.sink { _ in
            Task { @MainActor in statePublisher?.publish(state()) }
        }
        let sampler = DispatchSource.makeTimerSource(queue: .main)
        sampler.schedule(deadline: .now() + 1, repeating: 1)
        sampler.setEventHandler {
            guard mode == "full" else { return }
            accountRefreshSamples += 1
            if accountRefreshSamples >= 30 {
                accountRefreshSamples = 0
                scheduleAccountRefresh()
            }
            let player = ApplicationMusicPlayer.shared
            let playbackStatus = String(describing: player.state.playbackStatus)
            guard playbackStatus == "playing" else {
                stalledSamples = 0
                lastSampledPosition = nil
                lastSampledAt = nil
                statePublisher?.publish(state())
                return
            }
            let position = player.playbackTime
            let now = Date()
            if let lastPosition = lastSampledPosition, let lastAt = lastSampledAt {
                let elapsed = now.timeIntervalSince(lastAt)
                let delta = position - lastPosition
                if delta < 0 {
                    // Backward jumps are seeks or track changes, never stalls.
                    stalledSamples = 0
                } else if elapsed > 0.2 && delta < elapsed * 0.5 {
                    // Less than half the expected progress means audio stalled.
                    stalledSamples += 1
                } else {
                    stalledSamples = 0
                }
            } else {
                stalledSamples = 0
            }
            lastSampledPosition = position
            lastSampledAt = now
            statePublisher?.publish(state())
        }
        sampler.resume()
        progressSampler = sampler
    }

    static func observe(_ player: AVPlayer) {
        clearAVObservation()
        observedAVPlayer = player
        avTimeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(seconds: 1, preferredTimescale: 10), queue: .main) { _ in
            Task { @MainActor in statePublisher?.publish(state()) }
        }
        avStatusObserver = player.observe(\.timeControlStatus, options: [.initial, .new]) { _, _ in
            Task { @MainActor in statePublisher?.publish(state()) }
        }
        avItemStatusObserver = player.currentItem?.observe(\.status, options: [.initial, .new]) { item, _ in
            Task { @MainActor in
                if item.status == .failed {
                    playbackError = "Audio failed to load: \(item.error?.localizedDescription ?? "unknown AVPlayer error"). Check the stream URL and network, then retry."
                }
                statePublisher?.publish(state())
            }
        }
        if let item = player.currentItem {
            avFailureObserver = NotificationCenter.default.addObserver(forName: .AVPlayerItemFailedToPlayToEndTime, object: item, queue: .main) { note in
                Task { @MainActor in
                    let error = note.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? Error
                    playbackError = "Audio playback failed: \(error?.localizedDescription ?? "unknown AVPlayer error"). Check the stream URL and network, then retry."
                    statePublisher?.publish(state())
                }
            }
        }
    }

    static func clearAVObservation() {
        if let player = observedAVPlayer, let observer = avTimeObserver {
            player.removeTimeObserver(observer)
        }
        avStatusObserver = nil
        avItemStatusObserver = nil
        if let avFailureObserver { NotificationCenter.default.removeObserver(avFailureObserver) }
        avFailureObserver = nil
        avTimeObserver = nil
        observedAVPlayer = nil
    }

    static func dispatch(_ request: RPCRequest) async throws -> Result {
        switch request.method {
        case "ping": return .hello(Hello(pid: getpid()))
        case "debugAlbumSongs":
            guard let id = request.params?["id"]?.string else { throw PlayerError.invalidReference }
            let album = try await playableAlbum(id: id)
            let songs = try await albumSongs(album: album)
            return .tracks(songs.map { songTrack($0) })
        case "authorize":
            if request.params?["request"]?.bool == true && MusicAuthorization.currentStatus == .notDetermined {
                await MainActor.run { NSApplication.shared.activate(ignoringOtherApps: true) }
                _ = await MusicAuthorization.request()
            }
            let snapshot = authorization()
            scheduleAccountRefresh()
            return .authorization(snapshot)
        case "diagnose": return .diagnostics(await diagnoseTokens())
        case "libraryPlaylists": return .tracks(try await libraryPlaylists())
        case "libraryAlbums": return .tracks(try await libraryAlbums())
        case "recommendations": return .tracks(try await recommendations())
        case "playlistTracks":
            let playlistResult = try await playlistTracks(request.params)
            return .playlistTracks(playlistResult.playlist, playlistResult.tracks)
        case "albumTracks":
            let albumResult = try await albumTracks(request.params)
            return .albumTracks(albumResult.album, albumResult.songs)
        case "searchAlbums": return .tracks(try await searchAlbums(request.params))
        case "search": return .tracks(try await search(request.params))
        case "recentPlayed": return .tracks(try await recentPlayed(request.params))
        case "stations": return .tracks(try await stations(request.params))
        case "searchPlaylists": return .tracks(try await searchPlaylists(request.params))
        case "resolveUrl": return .tracks(try await resolveURL(request.params))
        case "play":
            try await play(request.params)
            let snapshot = state()
            Task { await cacheAvailableFormats() }
            return .state(snapshot)
        case "pause": pause(); return .state(state())
        case "resume": try await resume(); return .state(state())
        case "next", "previous":
            guard mode == "full" else { throw PlayerError.previewUnsupported }
            let step = request.method == "next" ? 1 : -1
            if request.method == "next" { try await ApplicationMusicPlayer.shared.skipToNextEntry() }
            else { try await ApplicationMusicPlayer.shared.skipToPreviousEntry() }
            // Advance the canonical cursor by the step we actually took: the
            // current entry's Song id may not exist in the canonical queue, so
            // the projection's id match fails and would otherwise keep the old
            // index and title while the audio moved on.
            queueCursor = advancedQueueCursor(queueCursor, count: queueSongs?.count ?? 0, step: step)
            return .state(state())
        case "setShuffle":
            ApplicationMusicPlayer.shared.state.shuffleMode = (request.params?["on"]?.bool ?? false) ? .songs : .off
            return .state(state())
        case "setRepeat":
            switch request.params?["mode"]?.string ?? "off" {
            case "all": ApplicationMusicPlayer.shared.state.repeatMode = .all
            case "one": ApplicationMusicPlayer.shared.state.repeatMode = .one
            default: ApplicationMusicPlayer.shared.state.repeatMode = MusicKit.MusicPlayer.RepeatMode.none
            }
            return .state(state())
        case "stop":
            stop()
            return .state(state())
        case "enqueue":
            try await enqueue(request.params)
            return .state(state())
        case "queueJump":
            try await queueJump(request.params)
            return .state(state())
        case "queueRemove":
            queueRemove(request.params)
            return .state(state())
        case "queueMove":
            queueMove(request.params)
            return .state(state())
        case "queueClear":
            queueClear()
            return .state(state())
        case "state":
            let snapshot = state()
            Task { await cacheAvailableFormats() }
            return .state(snapshot)
        case "shutdown": stopPlayback(); return .empty
        default: throw PlayerError.unknownMethod
        }
    }

    static func authorizationStatus() -> String {
        switch MusicAuthorization.currentStatus {
        case .authorized: return "authorized"
        case .denied: return "denied"
        case .restricted: return "restricted"
        case .notDetermined: return "not_determined"
        @unknown default: return "unknown"
        }
    }
    static func authorization() -> Authorization {
        let status = authorizationStatus()
        guard status == "authorized" else {
            accountStatus = nil
            accountError = nil
            accountCountryCode = nil
            accountCanPlayCatalogContent = false
            accountHasCloudLibraryEnabled = false
            return Authorization(status: status, accountStatus: nil, accountError: nil, countryCode: nil, canPlayCatalogContent: false, hasCloudLibraryEnabled: false)
        }
        return Authorization(status: status, accountStatus: accountStatus, accountError: accountError, countryCode: accountCountryCode, canPlayCatalogContent: accountCanPlayCatalogContent, hasCloudLibraryEnabled: accountHasCloudLibraryEnabled)
    }
    static func scheduleAccountRefresh() {
        guard authorizationStatus() == "authorized", accountRefreshTask == nil else { return }
        accountRefreshTask = Task { @MainActor in
            defer { accountRefreshTask = nil }
            do {
                let subscription = try await MusicSubscription.current
                guard !Task.isCancelled, authorizationStatus() == "authorized" else { return }
                accountCanPlayCatalogContent = subscription.canPlayCatalogContent
                accountHasCloudLibraryEnabled = subscription.hasCloudLibraryEnabled
                accountStatus = musicAccountStatus(canPlayCatalogContent: subscription.canPlayCatalogContent, hasCloudLibraryEnabled: subscription.hasCloudLibraryEnabled)
                accountError = nil
                statePublisher?.publish(state())

                if let countryCode = try? await MusicDataRequest.currentCountryCode,
                   !Task.isCancelled, authorizationStatus() == "authorized" {
                    accountCountryCode = countryCode
                    statePublisher?.publish(state())
                }
            } catch {
                guard !Task.isCancelled, authorizationStatus() == "authorized" else { return }
                accountCanPlayCatalogContent = false
                accountHasCloudLibraryEnabled = false
                accountStatus = "account_unavailable"
                accountError = errorDetails(error)
                statePublisher?.publish(state())
            }
        }
    }
    static func diagnoseTokens() async -> TokenDiagnostics {
        let provider = DefaultMusicTokenProvider()
        var developerTokenReceived = false
        var developerTokenError: String?
        var kid: String?
        var issuer: String?
        var issuedAt: Int?
        var expiresAt: Int?
        var userTokenReceived = false
        var userTokenError: String?
        var storefrontUSStatus: Int?
        var storefrontCNStatus: Int?

        do {
            let token = try await provider.developerToken(options: [.ignoreCache])
            let claims = tokenMetadata(token)
            developerTokenReceived = true
            (kid, issuer, issuedAt, expiresAt) = (claims.kid, claims.issuer, claims.issuedAt, claims.expiresAt)
            do {
                _ = try await provider.userToken(for: token, options: [.ignoreCache])
                userTokenReceived = true
            } catch {
                userTokenError = errorDetails(error)
            }
            async let us = storefrontStatus("us", token: token)
            async let cn = storefrontStatus("cn", token: token)
            (storefrontUSStatus, storefrontCNStatus) = await (us, cn)
        } catch {
            developerTokenError = errorDetails(error)
        }

        var subscriptionReceived = false
        var subscriptionError: String?
        var canPlayCatalogContent = false
        var hasCloudLibraryEnabled = false
        do {
            let subscription = try await MusicSubscription.current
            subscriptionReceived = true
            canPlayCatalogContent = subscription.canPlayCatalogContent
            hasCloudLibraryEnabled = subscription.hasCloudLibraryEnabled
        } catch {
            subscriptionError = errorDetails(error)
        }

        var countryCode: String?
        var countryCodeError: String?
        do { countryCode = try await MusicDataRequest.currentCountryCode }
        catch { countryCodeError = errorDetails(error) }

        var libraryPlaylistCount: Int?
        var libraryPlaylistError: String?
        do {
            var request = MusicLibraryRequest<Playlist>()
            request.limit = 1
            libraryPlaylistCount = try await request.response().items.count
        } catch {
            libraryPlaylistError = errorDetails(error)
        }

        return TokenDiagnostics(authorization: authorizationStatus(), bundleID: Bundle.main.bundleIdentifier, developerTokenReceived: developerTokenReceived, developerTokenError: developerTokenError, kid: kid, issuer: issuer, issuedAt: issuedAt, expiresAt: expiresAt, userTokenReceived: userTokenReceived, userTokenError: userTokenError, subscriptionReceived: subscriptionReceived, subscriptionError: subscriptionError, canPlayCatalogContent: canPlayCatalogContent, hasCloudLibraryEnabled: hasCloudLibraryEnabled, countryCode: countryCode, countryCodeError: countryCodeError, libraryPlaylistCount: libraryPlaylistCount, libraryPlaylistError: libraryPlaylistError, storefrontUSStatus: storefrontUSStatus, storefrontCNStatus: storefrontCNStatus)
    }
    static func errorDetails(_ error: Error) -> String {
        if let dataError = error as? MusicDataRequest.Error {
            return "MusicDataRequest status=\(dataError.status) code=\(dataError.code) \(dataError.title): \(dataError.detailText)"
        }
        let value = error as NSError
        return "\(String(reflecting: type(of: error))): \(String(describing: error)) [\(value.domain) \(value.code)]"
    }
    static func tokenMetadata(_ token: String) -> (kid: String?, issuer: String?, issuedAt: Int?, expiresAt: Int?) {
        let parts = token.split(separator: ".")
        guard parts.count == 3 else { return (nil, nil, nil, nil) }
        func object(_ part: Substring) -> [String: Any]? {
            var value = String(part).replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
            value += String(repeating: "=", count: (4 - value.count % 4) % 4)
            guard let data = Data(base64Encoded: value) else { return nil }
            return try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        }
        let header = object(parts[0])
        let payload = object(parts[1])
        return (header?["kid"] as? String, payload?["iss"] as? String, payload?["iat"] as? Int, payload?["exp"] as? Int)
    }
    static func storefrontStatus(_ storefront: String, token: String) async -> Int? {
        guard let url = URL(string: "https://api.music.apple.com/v1/storefronts/\(storefront)") else { return nil }
        var request = URLRequest(url: url)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            return (response as? HTTPURLResponse)?.statusCode
        } catch {
            return nil
        }
    }
    static func search(_ params: [String: JSONValue]?) async throws -> [Track] {
        guard let term = params?["term"]?.string, !term.isEmpty else { throw PlayerError.invalidSearch }
        let limit = max(1, min(params?["limit"]?.int ?? 20, 25))
        if authorizationStatus() != "authorized" { return try await iTunesSearch(term: term, limit: limit).map(iTunesTrack) }
        do {
            var request = MusicCatalogSearchRequest(term: term, types: [Song.self]); request.limit = limit
            let response = try await request.response()
            return response.songs.map { song in
                Track(kind: "song", id: song.id.rawValue, url: song.url?.absoluteString, title: song.title, artist: song.artistName, previewURL: song.previewAssets?.first?.url?.absoluteString)
            }
        } catch {
            fputs("MusicKit search unavailable; using Apple preview search: \(error.localizedDescription)\n", stderr)
            return try await iTunesSearch(term: term, limit: limit).map(iTunesTrack)
        }
    }
    static func libraryPlaylists() async throws -> [Track] {
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        var request = MusicLibraryRequest<Playlist>()
        request.limit = 100
        let response = try await request.response()
        return response.items.map { playlist in
            Track(kind: "playlist", id: playlist.id.rawValue, url: playlist.url?.absoluteString, title: playlist.name, artist: playlist.curatorName, previewURL: nil)
        }
    }
    static func libraryAlbums() async throws -> [Track] {
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        var request = MusicLibraryRequest<Album>()
        request.limit = 100
        let response = try await request.response()
        var tracks: [Track] = []
        for album in response.items {
            var full = album
            if full.title.isEmpty {
                // Library album entries can arrive as skeletons; a relationship
                // load re-fetches the item with its attributes.
                if let loaded = try? await album.with([.artists]) { full = loaded }
                if full.title.isEmpty { continue }
            }
            let artist = full.artistName.isEmpty ? nil : full.artistName
            tracks.append(Track(kind: "album", id: full.id.rawValue, url: full.url?.absoluteString, title: full.title, artist: artist, previewURL: nil))
        }
        return tracks
    }
    static func recommendations() async throws -> [Track] {
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        let request = MusicPersonalRecommendationsRequest()
        let response = try await request.response()
        var tracks: [Track] = []
        var seen = Set<String>()
        for recommendation in response.recommendations {
            for playlist in recommendation.playlists {
                let id = playlist.id.rawValue
                if seen.contains(id) { continue }
                seen.insert(id)
                tracks.append(Track(kind: "playlist", id: id, url: playlist.url?.absoluteString, title: playlist.name, artist: recommendation.title ?? recommendation.reason, previewURL: nil))
            }
            for station in recommendation.stations {
                let id = station.id.rawValue
                if seen.contains(id) { continue }
                seen.insert(id)
                tracks.append(Track(kind: "station", id: id, url: station.url?.absoluteString, title: station.name, artist: station.stationProviderName, previewURL: nil))
            }
        }
        return tracks
    }
    // albumTracks resolves an album's identity and track listing in one call:
    // the album row (kind "album") plus its songs. Library skeletons and
    // catalog albums both route through albumSongs' multi-fallback resolution.
    static func albumTracks(_ params: [String: JSONValue]?) async throws -> (album: Track, songs: [Track]) {
        guard let id = params?["id"]?.string, !id.isEmpty else { throw PlayerError.invalidReference }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        let album = try await playableAlbum(id: id)
        let artist = album.artistName
        let albumRow = Track(kind: "album", id: album.id.rawValue, url: album.url?.absoluteString, title: album.title, artist: artist, previewURL: nil)
        let songs = try await albumSongs(album: album).map { songTrack($0) }
        guard !songs.isEmpty else { throw PlayerError.invalidReference }
        return (albumRow, songs)
    }

    static func searchAlbums(_ params: [String: JSONValue]?) async throws -> [Track] {
        guard let term = params?["term"]?.string, !term.isEmpty else { throw PlayerError.invalidSearch }
        let limit = max(1, min(params?["limit"]?.int ?? 20, 25))
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        var request = MusicCatalogSearchRequest(term: term, types: [Album.self])
        request.limit = limit
        let response = try await request.response()
        return response.albums.map { album in
            Track(kind: "album", id: album.id.rawValue, url: album.url?.absoluteString, title: album.title, artist: album.artistName, previewURL: nil)
        }
    }

    // playlistTracks resolves a playlist's identity together with its tracks, the
    // same shape albumTracks uses: the caller needs the playlist's own name, and
    // only the helper holds the resolved Playlist object.
    static func playlistTracks(_ params: [String: JSONValue]?) async throws -> (playlist: Track, tracks: [Track]) {
        guard let id = params?["id"]?.string, !id.isEmpty else { throw PlayerError.invalidReference }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        if let catalog = try? await catalogPlaylist(id), !catalog.tracks.isEmpty {
            return catalog
        }
        if let library = try? await libraryPlaylist(id), !library.tracks.isEmpty {
            return library
        }
        throw PlayerError.invalidReference
    }
    static func playlistTrack(_ playlist: Playlist) -> Track {
        Track(kind: "playlist", id: playlist.id.rawValue, url: playlist.url?.absoluteString, title: playlist.name, artist: playlist.curatorName, previewURL: nil)
    }
    static func catalogPlaylist(_ id: String) async throws -> (playlist: Track, tracks: [Track])? {
        var request = MusicCatalogResourceRequest<Playlist>(matching: \.id, equalTo: MusicItemID(id))
        request.properties = [.entries]
        guard let playlist = try await request.response().items.first else { return nil }
        guard let entries = playlist.entries else { return nil }
        return (playlistTrack(playlist), entries.compactMap(entryTrack))
    }
    static func libraryPlaylist(_ id: String) async throws -> (playlist: Track, tracks: [Track])? {
        var request = MusicLibraryRequest<Playlist>()
        request.filter(matching: \.id, equalTo: MusicItemID(id))
        guard let playlist = try await request.response().items.first else { return nil }
        let row = playlistTrack(playlist)
        if let entries = playlist.entries, !entries.isEmpty { return (row, entries.compactMap(entryTrack)) }
        if let full = try? await playlist.with([.entries]), let entries = full.entries, !entries.isEmpty {
            return (row, entries.compactMap(entryTrack))
        }
        if let full = try? await playlist.with([.tracks]), let tracks = full.tracks, !tracks.isEmpty {
            let songs = tracks.compactMap { track -> Track? in
                guard case let .song(song) = track else { return nil }
                return songTrack(song)
            }
            return (row, songs)
        }
        return nil
    }
    static func entryTrack(_ entry: MusicKit.Playlist.Entry) -> Track? {
        // ApplicationMusicPlayer's song queue cannot represent music-video or
        // unavailable entries. Browse exposes exactly the same supported set
        // used by playlistSongs/playback so displayed ordering and startAt agree.
        guard case let .some(.song(song)) = entry.item else { return nil }
        return songTrack(song)
    }
    static func recentPlayed(_ params: [String: JSONValue]?) async throws -> [Track] {
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        let limit = max(1, min(params?["limit"]?.int ?? 25, 50))
        if !recentlyPlayedCloudUnavailable {
            do {
                var request = MusicRecentlyPlayedRequest<Song>()
                request.limit = limit
                let response = try await request.response()
                return response.items.map(songTrack)
            } catch {
                recentlyPlayedCloudUnavailable = true
                fputs("MusicKit recently played unavailable; using local library: \(errorDetails(error))\n", stderr)
            }
        }
        return try await libraryRecentlyPlayed(limit: limit)
    }
    static func libraryRecentlyPlayed(limit: Int) async throws -> [Track] {
        var request = MusicLibraryRequest<Song>()
        request.limit = limit
        request.sort(by: \.lastPlayedDate, ascending: false)
        let response = try await request.response()
        return response.items.filter { $0.lastPlayedDate != nil }.map(songTrack)
    }
    static func songTrack(_ song: Song) -> Track {
        Track(kind: "song", id: song.id.rawValue, url: song.url?.absoluteString, title: song.title, artist: song.artistName, previewURL: song.previewAssets?.first?.url?.absoluteString)
    }
    static func stations(_ params: [String: JSONValue]?) async throws -> [Track] {
        guard let term = params?["term"]?.string, !term.isEmpty else { throw PlayerError.invalidSearch }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        let limit = max(1, min(params?["limit"]?.int ?? 25, 50))
        var request = MusicCatalogSearchRequest(term: term, types: [Station.self]); request.limit = limit
        let response = try await request.response()
        return response.stations.map { station in
            Track(kind: "station", id: station.id.rawValue, url: station.url?.absoluteString, title: station.name, artist: station.stationProviderName, previewURL: nil)
        }
    }
    static func searchPlaylists(_ params: [String: JSONValue]?) async throws -> [Track] {
        guard let term = params?["term"]?.string, !term.isEmpty else { throw PlayerError.invalidSearch }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        let limit = max(1, min(params?["limit"]?.int ?? 25, 50))
        var request = MusicCatalogSearchRequest(term: term, types: [Playlist.self]); request.limit = limit
        let response = try await request.response()
        return response.playlists.map { Track(kind: "playlist", id: $0.id.rawValue, url: $0.url?.absoluteString, title: $0.name, artist: $0.curatorName, previewURL: nil) }
    }
    static func resolveURL(_ params: [String: JSONValue]?) async throws -> [Track] {
        guard let url = params?["url"]?.string, !url.isEmpty, let components = URLComponents(string: url), let id = canonicalID(PlaybackRequest(kind: "", id: nil, storefront: nil, url: url, startAt: nil, startTrackID: nil, reverse: nil, fromHere: nil)) else { throw PlayerError.invalidReference }
        let segments = components.path.split(separator: "/")
        var kind = segments.dropFirst().first.map(String.init) ?? "song"
        if kind == "album", components.queryItems?.first(where: { $0.name == "i" })?.value != nil { kind = "song" }
        if !["song", "playlist", "station", "album"].contains(kind) { kind = "song" }
        let track = Track(kind: kind, id: id, url: url, title: "", artist: nil, previewURL: nil)
        guard authorizationStatus() == "authorized" else { return [track] }
        do {
            if kind == "song" {
                let request = MusicCatalogResourceRequest<Song>(matching: \.id, equalTo: MusicItemID(id))
                if let song = try await request.response().items.first {
                    return [Track(kind: kind, id: song.id.rawValue, url: song.url?.absoluteString ?? url, title: song.title, artist: song.artistName, previewURL: song.previewAssets?.first?.url?.absoluteString)]
                }
            } else if kind == "playlist" {
                let request = MusicCatalogResourceRequest<Playlist>(matching: \.id, equalTo: MusicItemID(id))
                if let playlist = try await request.response().items.first {
                    return [Track(kind: kind, id: playlist.id.rawValue, url: playlist.url?.absoluteString ?? url, title: playlist.name, artist: playlist.curatorName, previewURL: nil)]
                }
            }
        } catch {}
        return [track]
    }
    static func play(_ params: [String: JSONValue]?) async throws {
        guard let params, let kind = params["kind"]?.string else { throw PlayerError.invalidReference }
        let request = PlaybackRequest(kind: kind, id: params["id"]?.string, storefront: params["storefront"]?.string, url: params["url"]?.string, startAt: params["startAt"]?.int, startTrackID: params["startTrackID"]?.string, reverse: params["reverse"]?.bool, fromHere: params["fromHere"]?.bool)
        guard ["song", "playlist", "station"].contains(request.kind), let id = canonicalID(request) else { throw PlayerError.invalidReference }
        clearAVObservation()
        playbackError = nil
        if authorizationStatus() != "authorized" {
            guard request.kind == "song" else { throw PlayerError.authorizationRequired }
            clearCanonicalQueue()
            try await playPreview(id: id)
            return
        }
        do {
            if request.kind == "playlist" {
                var songs = try await playlistSongs(id)
                if request.reverse == true { songs.reverse() }
                guard !songs.isEmpty else { throw PlayerError.invalidReference }
                let descriptors = songs.map { StartTrack(id: $0.id.rawValue) }
                var startIndex = selectedStartIndex(tracks: descriptors, id: request.startTrackID, index: request.startAt)
                if request.fromHere == true {
                    // "Play from here" is forward-only: drop the tracks before the
                    // selection instead of keeping them as history.
                    songs = Array(songs[startIndex...])
                    startIndex = 0
                }
                currentTrack = songTrack(songs[startIndex])
                previewPlayer?.pause(); mode = "full"
                let player = ApplicationMusicPlayer.shared
                let entries = songs.map { ApplicationMusicPlayer.Queue.Entry($0) }
                player.queue = .init(entries, startingAt: entries[startIndex])
                preferQueueWalk = false
                installCanonicalQueue(songs, currentIndex: startIndex)
            } else if request.kind == "station" {
                let catalog = MusicCatalogResourceRequest<Station>(matching: \.id, equalTo: MusicItemID(id))
                guard let station = try await catalog.response().items.first else { throw PlayerError.invalidReference }
                currentTrack = Track(kind: "station", id: station.id.rawValue, url: station.url?.absoluteString, title: station.name, artist: station.stationProviderName, previewURL: nil)
                previewPlayer?.pause(); mode = "full"
                ApplicationMusicPlayer.shared.queue = .init(for: [station])
                clearCanonicalQueue()
            } else {
                var song = try? await catalogSong(id)
                if song == nil { song = try? await librarySong(id) }
                guard let song else { throw PlayerError.invalidReference }
                currentTrack = Track(kind: "song", id: song.id.rawValue, url: song.url?.absoluteString, title: song.title, artist: song.artistName, previewURL: song.previewAssets?.first?.url?.absoluteString)
                previewPlayer?.pause(); mode = "full"
                ApplicationMusicPlayer.shared.queue = .init(for: [song])
                preferQueueWalk = false
                installCanonicalQueue([song], currentIndex: 0)
            }
            try await ApplicationMusicPlayer.shared.play()
        } catch {
            guard request.kind == "song" else { throw error }
            stopMusicAndWaitForSilence()
            fputs("MusicKit full playback unavailable; using preview: \(errorDetails(error))\n", stderr)
            do {
                try await playPreview(id: id)
            } catch let previewError {
                // Preview failed too: surface the full-playback failure, which
                // is the real cause, instead of the preview error that would
                // mislead ("no usable preview asset" for a song that does not
                // even exist).
                throw NSError(domain: "lilt", code: 3, userInfo: [
                    NSLocalizedDescriptionKey: "full playback failed: \(errorDetails(error)); preview also unavailable: \(errorDetails(previewError))",
                ])
            }
        }
    }
    static func playableAlbum(id: String) async throws -> Album {
        var library = MusicLibraryRequest<Album>()
        library.filter(matching: \.id, equalTo: MusicItemID(id))
        if let album = try await library.response().items.first { return album }
        let catalog = MusicCatalogResourceRequest<Album>(matching: \.id, equalTo: MusicItemID(id))
        guard let album = try await catalog.response().items.first else { throw PlayerError.invalidReference }
        return album
    }
    // albumSongs resolves an album's track listing. Library skeletons need a
    // relationship load; catalog albums already carry it.
    static func albumSongs(album: Album) async throws -> [Song] {        // A library album entity often ships without a tracks relationship and
        // the user's library may hold only some songs. Try the library first
        // (fully local playback), then resolve the catalog album by title and
        // load its track listing.
        var librarySongs = MusicLibraryRequest<Song>()
        librarySongs.filter(matching: \.albumTitle, equalTo: album.title)
        if let response = try? await librarySongs.response(), response.items.count > 1 {
            return response.items.map { $0 }
        }
        if let loaded = try? await album.with([.tracks]), let tracks = loaded.tracks, tracks.count > 1 {
            var songs: [Song] = []
            for track in tracks {
                if case .song(let song) = track { songs.append(song) }
            }
            if !songs.isEmpty { return songs }
        }
        var search = MusicCatalogSearchRequest(term: album.title + " " + album.artistName, types: [Album.self])
        search.limit = 5
        if let response = try? await search.response() {
            for match in response.albums where match.title == album.title {
                if let loaded = try? await match.with([.tracks]) {
                    var songs: [Song] = []
                    if let tracks = loaded.tracks {
                        for track in tracks {
                            if case .song(let song) = track { songs.append(song) }
                        }
                    }
                    if songs.count > 1 { return songs }
                }
            }
        }
        return albumTracks(album)
    }
    static func albumTracks(_ album: Album) -> [Song] {
        var songs: [Song] = []
        if let tracks = album.tracks {
            for track in tracks {
                if case .song(let song) = track { songs.append(song) }
            }
        }
        return songs
    }
    static func catalogSong(_ id: String) async throws -> Song? {
        let request = MusicCatalogResourceRequest<Song>(matching: \.id, equalTo: MusicItemID(id))
        return try await request.response().items.first
    }
    static func librarySong(_ id: String) async throws -> Song? {
        var request = MusicLibraryRequest<Song>()
        request.filter(matching: \.id, equalTo: MusicItemID(id))
        return try await request.response().items.first
    }
    static func playlistSongs(_ id: String) async throws -> [Song] {
        var catalog = MusicCatalogResourceRequest<Playlist>(matching: \.id, equalTo: MusicItemID(id))
        catalog.properties = [.entries]
        if let playlist = try? await catalog.response().items.first,
           let entries = playlist.entries {
            let songs = entries.compactMap { entry -> Song? in
                guard case let .some(.song(song)) = entry.item else { return nil }
                return song
            }
            if !songs.isEmpty { return songs }
        }

        var library = MusicLibraryRequest<Playlist>()
        library.filter(matching: \.id, equalTo: MusicItemID(id))
        if let playlist = try await library.response().items.first {
            if let full = try? await playlist.with([.entries]), let entries = full.entries {
                let songs = entries.compactMap { entry -> Song? in
                    guard case let .some(.song(song)) = entry.item else { return nil }
                    return song
                }
                if !songs.isEmpty { return songs }
            }
            if let full = try? await playlist.with([.tracks]), let tracks = full.tracks, !tracks.isEmpty {
                let songs = tracks.compactMap { track -> Song? in
                    guard case let .song(song) = track else { return nil }
                    return song
                }
                if !songs.isEmpty { return songs }
            }
        }
        throw PlayerError.invalidReference
    }
    static func enqueue(_ params: [String: JSONValue]?) async throws {
        guard let params, let kind = params["kind"]?.string else { throw PlayerError.invalidReference }
        let request = PlaybackRequest(kind: kind, id: params["id"]?.string, storefront: params["storefront"]?.string, url: params["url"]?.string, startAt: nil, startTrackID: nil, reverse: nil, fromHere: nil)
        guard ["song", "playlist", "station", "album"].contains(request.kind), let id = canonicalID(request) else { throw PlayerError.invalidReference }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        guard !ApplicationMusicPlayer.shared.queue.entries.isEmpty else { throw PlayerError.queueUnavailable }
        let position: MusicKit.MusicPlayer.Queue.EntryInsertionPosition = params["position"]?.string == "next" ? .afterCurrentEntry : .tail
        if request.kind == "playlist" {
            var library = MusicLibraryRequest<Playlist>()
            library.filter(matching: \.id, equalTo: MusicItemID(id))
            if let playlist = try await library.response().items.first {
                try await ApplicationMusicPlayer.shared.queue.insert(playlist, position: position)
                clearCanonicalQueue()
                return
            }
            let catalog = MusicCatalogResourceRequest<Playlist>(matching: \.id, equalTo: MusicItemID(id))
            guard let playlist = try await catalog.response().items.first else { throw PlayerError.invalidReference }
            try await ApplicationMusicPlayer.shared.queue.insert(playlist, position: position)
            clearCanonicalQueue()
        } else if request.kind == "album" {
            let album = try await playableAlbum(id: id)
            try await ApplicationMusicPlayer.shared.queue.insert(album, position: position)
        } else if request.kind == "station" {
            let catalog = MusicCatalogResourceRequest<Station>(matching: \.id, equalTo: MusicItemID(id))
            guard let station = try await catalog.response().items.first else { throw PlayerError.invalidReference }
            try await ApplicationMusicPlayer.shared.queue.insert(station, position: position)
            clearCanonicalQueue()
        } else {
            var song = try? await catalogSong(id)
            if song == nil { song = try? await librarySong(id) }
            guard let song else { throw PlayerError.invalidReference }
            try await ApplicationMusicPlayer.shared.queue.insert(song, position: position)
            if var songs = queueSongs {
                let anchor = currentSongIndex(songs)
                let insertAt = position == .afterCurrentEntry ? min(anchor + 1, songs.count) : songs.count
                songs.insert(song, at: insertAt)
                queueSongs = songs
                preferQueueWalk = true
            }
        }
    }
    static func stop() {
        previewPlayer?.pause()
        ApplicationMusicPlayer.shared.stop()
        clearAVObservation()
        mode = "none"
        currentTrack = nil
        previewPlayer = nil
        playbackError = nil
        ApplicationMusicPlayer.shared.queue.entries = .init()
        clearCanonicalQueue()
    }
    // queueJump starts playback at the chosen queue entry. The index refers to
    // the canonical submitted order (queueSongs) — the same array the client
    // renders — never to MusicKit's possibly shuffled live entries. The queue
    // is rebuilt from those songs exactly like the initial play: MusicKit
    // accepts fresh Queue.Entry values but rejects entries captured from the
    // live queue with "unexpected start item" (Code 6). Under shuffle the
    // rebuild can still land on a different entry, so the current entry is
    // verified and the start re-pinned with shuffle briefly off; a failed
    // replacement restores the previous queue before falling back to stepping,
    // so a failed jump never leaves a half-replaced queue behind.
    static func debugLog(_ message: String) {
        let path = "/tmp/lilt-player-debug.log"
        let line = "\(Date().timeIntervalSince1970): \(message)\n"
        if let handle = FileHandle(forWritingAtPath: path) {
            defer { try? handle.close() }
            handle.seekToEndOfFile()
            if let data = line.data(using: .utf8) { handle.write(data) }
        } else {
            try? line.write(toFile: path, atomically: true, encoding: .utf8)
        }
    }
    static func titleOfCurrentEntry() -> String {
        guard let current = ApplicationMusicPlayer.shared.queue.currentEntry,
              case .song(let song)? = current.item else { return "unresolved/nil" }
        return song.title + "/" + song.id.rawValue
    }
    static func queueJump(_ params: [String: JSONValue]?) async throws {
        guard mode == "full" else { throw PlayerError.previewUnsupported }
        let player = ApplicationMusicPlayer.shared
        guard let index = params?["index"]?.int else { throw PlayerError.invalidReference }
        debugLog("queueJump index=\(index) songs=\(queueSongs?.count ?? -1) entries=\(player.queue.entries.count) currentPayload=\(titleOfCurrentEntry())")

        if let songs = queueSongs, songs.indices.contains(index) {
            // A queue MusicKit built by appending entries cannot be rebuilt:
            // assigning one back fails with Code=6 "Failed to prepare to play"
            // AND takes the live queue down with it, so attempting the rebuild
            // would stop the user's playback for nothing. Refuse the jump and
            // keep playing. The same row can still be started from the album
            // detail, which re-runs the play path (batch 2026-09-20-search-and-queue N6).
            if preferQueueWalk {
                throw jumpFailure(
                    target: index,
                    count: songs.count,
                    underlying: "MusicKit cannot rebuild a queue built track by track",
                    playbackRestored: true,
                )
            }
            let fresh = songs.map { ApplicationMusicPlayer.Queue.Entry($0) }
            let original = currentSongIndex(songs)
            // Rebuild with shuffle off so MusicKit honors startingAt, then
            // restore shuffle so the remaining pass stays random. Replacing
            // the queue while shuffle is already on can land on a different
            // entry (or pause), which is how clicks stopped matching tracks.
            let shuffled = player.state.shuffleMode == .songs
            player.state.shuffleMode = .off
            player.queue = .init(fresh, startingAt: fresh[index])
            do {
                try await player.play()
                preferQueueWalk = false
                installCanonicalQueue(songs, currentIndex: index)
                if shuffled { player.state.shuffleMode = .songs }
                return
            } catch {
                if shuffled { player.state.shuffleMode = .songs }
                // MusicKit rejects the rebuild of a queue it built by appending
                // (Code=6 "Failed to prepare to play") and takes the live queue
                // down with it. Restore what the user was listening to first — a
                // failed jump must never stop playback. Walking with
                // skipToNextEntry is not a substitute: MusicKit skips entries it
                // cannot prepare, so the walk lands past the row the user chose
                // (measured: target 4, playback 6 in batch
                // 2026-09-20-search-and-queue). Report the failure instead.
                debugLog("queueJump rebuild rejected: \(errorDetails(error)); restoring entry \(original)")
                let restored = await restoreCanonicalQueue(player, entries: fresh, songs: songs, currentIndex: original)
                if !preferQueueWalk, await step(player, to: index), landedOn(player, song: songs[index]) {
                    installCanonicalQueue(songs, currentIndex: index)
                    return
                }
                throw jumpFailure(target: index, count: songs.count, underlying: errorDetails(error), playbackRestored: restored)
            }
        }
        let entries = Array(player.queue.entries)
        guard entries.indices.contains(index) else {
            throw jumpFailure(target: index, count: entries.count, underlying: "the queue holds \(entries.count) entries", playbackRestored: true)
        }
        if await step(player, to: index) { return }
        throw jumpFailure(target: index, count: entries.count, underlying: "stepping could not reach the entry", playbackRestored: true)
    }

    // landedOn verifies a walk ended on the entry the user chose: MusicKit skips
    // entries it cannot prepare, so a walk can report success on a later row.
    private static func landedOn(_ player: ApplicationMusicPlayer, song: Song) -> Bool {
        currentSongID(player.queue.currentEntry) == song.id.rawValue
    }

    // restoreCanonicalQueue puts a rebuilt queue back on a canonical position and
    // resumes it, so a rejected jump leaves the previous track playing. It
    // reports whether resume actually took.
    private static func restoreCanonicalQueue(_ player: ApplicationMusicPlayer, entries: [ApplicationMusicPlayer.Queue.Entry], songs: [Song], currentIndex: Int) async -> Bool {
        guard !entries.isEmpty else { return false }
        let index = min(max(currentIndex, 0), entries.count - 1)
        player.queue = .init(entries, startingAt: entries[index])
        preferQueueWalk = true
        installCanonicalQueue(songs, currentIndex: index)
        do {
            try await player.play()
            return true
        } catch {
            debugLog("queueJump restore failed: \(errorDetails(error))")
            return false
        }
    }

    // jumpFailure reports a jump MusicKit refused. It is deliberately not
    // invalidReference: the reference was fine and the player refused the
    // rebuild. The message states whether the previous track kept playing.
    private static func jumpFailure(target: Int, count: Int, underlying: String, playbackRestored: Bool) -> NSError {
        let tail = playbackRestored
            ? "Playback continues with the current track; open the album or playlist and start from that row instead."
            : "Playback stopped; press p to start it again."
        return NSError(domain: "lilt", code: 1, userInfo: [
            NSLocalizedDescriptionKey: "could not jump to row \(target + 1) of \(count): \(underlying). \(tail)",
        ])
    }
    // step moves the existing queue to the target entry one skip at a time.
    private static func step(_ player: ApplicationMusicPlayer, to target: Int) async -> Bool {
        func position() -> Int? {
            guard let current = player.queue.currentEntry else { return nil }
            return player.queue.entries.firstIndex { $0.id == current.id }
        }
        var current = position()
        var hops = 0
        while let value = current, value != target, hops < 500 {
            hops += 1
            do {
                if value < target { try await player.skipToNextEntry() } else { try await player.skipToPreviousEntry() }
            } catch {
                return false
            }
            current = position()
        }
        guard current == target else { return false }
        do {
            try await player.play()
            return true
        } catch {
            return false
        }
    }
    static func queueRemove(_ params: [String: JSONValue]?) {
        guard mode == "full" else { return }
        let player = ApplicationMusicPlayer.shared
        guard let index = params?["index"]?.int else { return }
        if let songs = queueSongs, let remaining = removedQueue(songs, at: index) {
            var entries = player.queue.entries
            guard let offset = liveEntryOffset(
                entrySongIDs: entries.map(currentSongID),
                songID: songs[index].id.rawValue,
                canonicalIndex: index,
            ) else { return }
            entries.remove(at: offset)
            player.queue.entries = entries
            queueSongs = remaining
            if queueCursor > index { queueCursor -= 1 }
            else if queueCursor == index { queueCursor = min(index, max(0, remaining.count - 1)) }
            if remaining.indices.contains(queueCursor) { currentTrack = songTrack(remaining[queueCursor]) }
            return
        }
        var entries = player.queue.entries
        guard entries.indices.contains(index) else { return }
        entries.remove(at: index)
        player.queue.entries = entries
    }
    static func queueMove(_ params: [String: JSONValue]?) {
        guard mode == "full" else { return }
        let player = ApplicationMusicPlayer.shared
        var entries = player.queue.entries
        guard let from = params?["from"]?.int, let to = params?["to"]?.int else { return }
        if let songs = queueSongs, let reordered = movedQueue(songs, from: from, to: to) {
            // Shuffled playback ignores live array order, so only the canonical
            // order moves; otherwise reorder the matching live entry too.
            if player.state.shuffleMode != .songs,
               let offset = liveEntryOffset(
                   entrySongIDs: entries.map(currentSongID),
                   songID: songs[from].id.rawValue,
                   canonicalIndex: from,
               ) {
                let entry = entries.remove(at: offset)
                let destination = min(to, entries.count)
                entries.insert(entry, at: destination)
                player.queue.entries = entries
            }
            queueSongs = reordered
            queueCursor = movedCanonicalIndex(queueCursor, from: from, to: to)
            if reordered.indices.contains(queueCursor) { currentTrack = songTrack(reordered[queueCursor]) }
            return
        }
        guard entries.indices.contains(from) else { return }
        let entry = entries.remove(at: from)
        entries.insert(entry, at: min(to, entries.count))
        player.queue.entries = entries
    }
    static func queueClear() {
        previewPlayer?.pause()
        ApplicationMusicPlayer.shared.stop()
        ApplicationMusicPlayer.shared.queue.entries = .init()
        clearCanonicalQueue()
        mode = "none"
        currentTrack = nil
        playbackError = nil
    }

    private static func installCanonicalQueue(_ songs: [Song], currentIndex: Int) {
        queueSongs = songs
        queueCursor = min(max(currentIndex, 0), max(0, songs.count - 1))
        if songs.indices.contains(queueCursor) { currentTrack = songTrack(songs[queueCursor]) }
    }

    private static func clearCanonicalQueue() {
        queueSongs = nil
        preferQueueWalk = false
        queueCursor = 0
    }


    private static func currentSongID(_ entry: ApplicationMusicPlayer.Queue.Entry?) -> String? {
        guard let entry, case .song(let song)? = entry.item else { return nil }
        return song.id.rawValue
    }

    private static func currentSongIndex(_ songs: [Song]) -> Int {
        let current = ApplicationMusicPlayer.shared.queue.currentEntry
        let index = canonicalQueueIndex(
            ids: songs.map { $0.id.rawValue },
            currentSongID: currentSongID(current),
            fallbackIndex: queueCursor,
        )
        queueCursor = index
        return index
    }

    private static func movedCanonicalIndex(_ index: Int, from: Int, to: Int) -> Int {
        if index == from { return to }
        if from < to && index > from && index <= to { return index - 1 }
        if to < from && index >= to && index < from { return index + 1 }
        return index
    }
    static func canonicalID(_ request: PlaybackRequest) -> String? {
        if let id = request.id, !id.isEmpty { return id.contains(":") ? String(id.split(separator: ":", maxSplits: 1)[1]) : id }
        guard let url = request.url, let components = URLComponents(string: url), components.host?.hasSuffix("music.apple.com") == true else { return nil }
        return components.queryItems?.first(where: { $0.name == "i" })?.value ?? components.path.split(separator: "/").last.map(String.init)
    }
    static func pause() {
        if mode == "full" { ApplicationMusicPlayer.shared.pause() }
        else { previewPlayer?.pause() }
    }
    static func resume() async throws {
        if mode == "full" { try await ApplicationMusicPlayer.shared.play() }
        else if let previewPlayer { previewPlayer.play() }
        else { throw PlayerError.nothingPlaying }
    }
    static func stopPlayback() {
        previewPlayer?.pause()
        ApplicationMusicPlayer.shared.pause()
        clearAVObservation()
        mode = "none"
        currentTrack = nil
        previewPlayer = nil
        playbackError = nil
        ApplicationMusicPlayer.shared.queue.entries = .init()
        clearCanonicalQueue()
    }
    static func publishCurrentState() {
        statePublisher?.publish(state())
    }

    static func state() -> State {
        if mode == "full" {
            let player = ApplicationMusicPlayer.shared
            let current = player.queue.currentEntry
            var queue: [Track] = []
            var index = 0
            let track: Track?
            if let songs = queueSongs, !songs.isEmpty {
                // Reported order and indices are the submitted song order, the
                // same space queue.jump/remove/move use. MusicKit's live queue
                // may shuffle, and its current Song payload is not reliably
                // comparable with the resolved Song id, so prefer the stable
                // Queue.Entry.id mapping created with this queue.
                queue = songs.map(songTrack)
                index = currentSongIndex(songs)
                track = songTrack(songs[index])
                currentTrack = track
                debugLog("state projection: queueSongs=\(songs.count) song=\(currentSongID(current) ?? "nil") index=\(index) status=\(fullPlaybackStatus(player)) pos=\(player.playbackTime)")
            } else {
                for (offset, entry) in player.queue.entries.enumerated() {
                    if entry.id == current?.id { index = offset }
                    queue.append(queueTrack(entry))
                }
                track = current.map(queueTrack) ?? currentTrack
            }
            return State(track: track, position: player.playbackTime, duration: duration(of: current) ?? 0, status: playbackError == nil ? fullPlaybackStatus(player) : "error", audioVariant: player.state.audioVariant.map { String(describing: $0) }, format: formatLabel(player.state.audioVariant), availableFormats: availableFormats(for: track?.id), shuffle: player.state.shuffleMode == .songs, repeatMode: repeatLabel(player.state.repeatMode), isLive: false, mode: mode, authorization: authorizationStatus(), accountStatus: accountStatus, accountError: accountError, playbackError: playbackError, queue: queue, queueIndex: index)
        }
        let seconds = previewPlayer?.currentTime().seconds ?? 0
        let status: String
        if mode == "none" { status = "stopped" }
        else { switch previewPlayer?.timeControlStatus { case .playing: status = "playing"; case .waitingToPlayAtSpecifiedRate: status = "buffering"; default: status = "paused" } }
        let previewFormat = mode == "preview" ? "AAC preview" : "—"
        return State(track: currentTrack, position: seconds.isFinite ? seconds : 0, duration: 0, status: playbackError == nil ? status : "error", audioVariant: nil, format: previewFormat, availableFormats: [], shuffle: false, repeatMode: "off", isLive: false, mode: mode, authorization: authorizationStatus(), accountStatus: accountStatus, accountError: accountError, playbackError: playbackError, queue: [], queueIndex: 0)
    }
    // MusicKit keeps reporting "playing" while audio is stalled; expose a
    // buffering status as soon as one sample shows less than half the expected
    // positional progress (detection latency stays under the 1s sample interval).
    static func fullPlaybackStatus(_ player: ApplicationMusicPlayer) -> String {
        let raw = String(describing: player.state.playbackStatus)
        if raw == "playing" && stalledSamples >= 1 {
            return "buffering"
        }
        return raw
    }
    static func musicIsPlaying() -> Bool {
        String(describing: ApplicationMusicPlayer.shared.state.playbackStatus) == "playing"
    }

    // Preview fallback cannot buffer quietly, so it waits (bounded) instead.
    static func stopMusicAndWaitForSilence(timeout: TimeInterval = 3) {
        let player = ApplicationMusicPlayer.shared
        let wasPlaying = musicIsPlaying()
        player.stop()
        guard wasPlaying else { return }
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if !musicIsPlaying() { return }
            usleep(50_000)
        }
    }

    static func repeatLabel(_ repeatMode: MusicKit.MusicPlayer.RepeatMode?) -> String {
        switch repeatMode {
        case .all: return "all"
        case .one: return "one"
        default: return "off"
        }
    }
    static func availableFormats(for id: String?) -> [String] {
        guard let id, let cached = variantCache[id] else { return [] }
        return cached
    }
    static func cacheAvailableFormats() async {
        guard case .song(let song)? = ApplicationMusicPlayer.shared.queue.currentEntry?.item else { return }
        let id = song.id.rawValue
        if variantCache[id] != nil || variantInFlight.contains(id) { return }
        variantInFlight.insert(id)
        defer { variantInFlight.remove(id) }
        if let variants = song.audioVariants {
            variantCache[id] = variants.map { formatLabel($0) }
            return
        }
        do {
            var request = MusicCatalogResourceRequest<Song>(matching: \.id, equalTo: song.id)
            request.properties = [.audioVariants]
            if let full = try await request.response().items.first, let variants = full.audioVariants {
                variantCache[id] = variants.map { formatLabel($0) }
            }
        } catch {}
    }
    static func formatLabel(_ variant: MusicKit.AudioVariant?) -> String {
        // MusicKit may not expose the chosen variant for full playback. This
        // means the system selected the stream; it must not be mistaken for AAC.
        guard let variant else { return "System-selected" }
        switch variant {
        case .lossless: return "ALAC Lossless · up to 24/48"
        case .highResolutionLossless: return "ALAC Hi-Res Lossless · up to 24/192"
        case .dolbyAtmos: return "Dolby Atmos"
        case .dolbyAudio: return "Dolby Audio"
        case .spatialAudio: return "Spatial Audio"
        case .lossyStereo: return "AAC 256 kbps"
        @unknown default: return String(describing: variant)
        }
    }
    static func queueTrack(_ entry: MusicKit.MusicPlayer.Queue.Entry) -> Track {
        var id: String?
        var kind = "song"
        if case .song(let song)? = entry.item { id = song.id.rawValue }
        else if case .musicVideo(let video)? = entry.item { id = video.id.rawValue; kind = "musicVideo" }
        return Track(kind: kind, id: id, url: nil, title: entry.title, artist: entry.subtitle, previewURL: nil)
    }
    static func duration(of entry: MusicKit.MusicPlayer.Queue.Entry?) -> Double? {
        guard let entry else { return nil }
        if case .song(let song)? = entry.item { return song.duration }
        return nil
    }
    static func iTunesSearch(term: String, limit: Int) async throws -> [ITunesSong] {
        var components = URLComponents(string: "https://itunes.apple.com/search")!
        components.queryItems = [URLQueryItem(name: "term", value: term), URLQueryItem(name: "media", value: "music"), URLQueryItem(name: "entity", value: "song"), URLQueryItem(name: "limit", value: String(limit))]
        guard let url = components.url else { throw PlayerError.invalidSearch }
        let (data, response) = try await URLSession.shared.data(from: url)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw PlayerError.previewSearchUnavailable }
        return try JSONDecoder().decode(ITunesSearchResponse.self, from: data).results
    }
    static func iTunesLookup(id: String) async throws -> ITunesSong? {
        var components = URLComponents(string: "https://itunes.apple.com/lookup")!
        components.queryItems = [URLQueryItem(name: "id", value: id), URLQueryItem(name: "entity", value: "song")]
        guard let url = components.url else { throw PlayerError.invalidReference }
        let (data, response) = try await URLSession.shared.data(from: url)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw PlayerError.previewSearchUnavailable }
        return try JSONDecoder().decode(ITunesSearchResponse.self, from: data).results.first
    }
    static func playPreview(id: String) async throws {
        guard let song = try await iTunesLookup(id: id), let preview = song.previewUrl, let url = URL(string: preview), url.scheme == "https" else { throw PlayerError.previewUnavailable }
        currentTrack = iTunesTrack(song)
        playbackError = nil
        previewPlayer?.pause()
        mode = "preview"
        previewPlayer = AVPlayer(url: url)
        if let previewPlayer { observe(previewPlayer) }
        previewPlayer?.play()
    }
    static func iTunesTrack(_ song: ITunesSong) -> Track {
        Track(kind: "song", id: String(song.trackId), url: song.trackViewUrl, title: song.trackName, artist: song.artistName, previewURL: song.previewUrl)
    }
}

enum PlayerError: LocalizedError {
    case invalidReference, invalidSearch, previewUnavailable, previewSearchUnavailable, previewUnsupported, authorizationRequired, queueUnavailable, nothingPlaying, unknownMethod
    var code: String {
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
        }
    }
    var errorDescription: String? {
        switch self {
        case .invalidReference: return "play requires a catalog song reference"
        case .invalidSearch: return "search requires a non-empty term"
        case .previewUnavailable: return "this catalog song has no usable preview asset"
        case .previewSearchUnavailable: return "Apple preview search is unavailable"
        case .previewUnsupported: return "next and previous are unavailable in preview mode"
        case .authorizationRequired: return "Apple Music authorization is required"
        case .queueUnavailable: return "nothing is playing yet; start playback before queueing"
        case .nothingPlaying: return "nothing is playing to resume"
        case .unknownMethod: return "unknown JSON-RPC method"
        }
    }
}
