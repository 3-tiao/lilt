import AppKit
import AVFoundation
import Combine
import Darwin
import Foundation
import MediaPlayer
import MusicKit
import LiltPlayerLogic

final class FreshMusicTokenProvider: MusicUserTokenProvider, MusicDeveloperTokenProvider, @unchecked Sendable {
    private let provider = DefaultMusicTokenProvider()

    override init() { super.init() }

    func developerToken(options: MusicTokenRequestOptions) async throws -> String {
        try await provider.developerToken(options: options.union(.ignoreCache))
    }
}

struct PlaybackRequest: Codable { let kind: String; let id: String?; let storefront: String?; let url: String?; let startAt: Int?; let startTrackID: String?; let startTitle: String?; let reverse: Bool? }
struct JSONValue: Codable {
    private let storage: Storage
    private enum Storage { case string(String), int(Int), bool(Bool), object([String: JSONValue]), array([JSONValue]), null }
    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { storage = .null }
        else if let value = try? c.decode(Bool.self) { storage = .bool(value) }
        else if let value = try? c.decode(Int.self) { storage = .int(value) }
        else if let value = try? c.decode(String.self) { storage = .string(value) }
        else if let value = try? c.decode([JSONValue].self) { storage = .array(value) }
        else { storage = .object(try c.decode([String: JSONValue].self)) }
    }
    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch storage {
        case .string(let value): try c.encode(value)
        case .int(let value): try c.encode(value)
        case .bool(let value): try c.encode(value)
        case .object(let value): try c.encode(value)
        case .array(let value): try c.encode(value)
        case .null: try c.encodeNil()
        }
    }
    var string: String? { if case .string(let value) = storage { return value }; return nil }
    var int: Int? { if case .int(let value) = storage { return value }; return nil }
    var bool: Bool? { if case .bool(let value) = storage { return value }; return nil }
    var array: [JSONValue]? { if case .array(let value) = storage { return value }; return nil }
}
struct RPCRequest: Codable { let jsonrpc: String; let id: Int; let method: String; let params: [String: JSONValue]? }
struct RPCError: Codable { let code: String; let message: String }
struct Authorization: Codable {
    let status: String
    let accountStatus: String?
    let accountError: String?
    let countryCode: String?
    let canPlayCatalogContent: Bool
    let hasCloudLibraryEnabled: Bool
}
struct Hello: Codable { let pid: Int32 }
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
struct State: Codable, Sendable { let track: Track?; let position: Double; let duration: Double; let status: String; let audioVariant: String?; let format: String; let availableFormats: [String]; let shuffle: Bool; let repeatMode: String; let isLive: Bool; let mode: String; let authorization: String; let accountStatus: String?; let accountError: String?; let playbackError: String?; let queue: [Track]; let queueIndex: Int }
struct ProbeResult: Encodable, Sendable {
    let status: String
    let latencyMs: Int?
    let errorCode: String?
    let message: String?
}

// HTTPFirstByteProbe owns its URLSession delegate until it resolves exactly one
// result. The timer starts immediately before task.resume(), so redirects and
// TLS negotiation are included in the measured first-byte latency.
final class HTTPFirstByteProbe: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private var continuation: CheckedContinuation<ProbeResult, Never>?
    private var session: URLSession?
    private var task: URLSessionDataTask?
    private var started: Date?
    private var statusCode: Int?
    private var receivedData = false
    private var cancelledAfterFirstByte = false

    func run(request: URLRequest) async -> ProbeResult {
        await withCheckedContinuation { continuation in
            self.continuation = continuation
            let configuration = URLSessionConfiguration.ephemeral
            configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
            configuration.urlCache = nil
            configuration.timeoutIntervalForRequest = request.timeoutInterval
            configuration.timeoutIntervalForResource = request.timeoutInterval
            self.session = URLSession(configuration: configuration, delegate: self, delegateQueue: nil)
            let task = self.session!.dataTask(with: request)
            self.task = task
            self.started = Date()
            task.resume()
        }
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) {
        statusCode = (response as? HTTPURLResponse)?.statusCode
        if let statusCode, probeHTTPResponseOutcome(statusCode: statusCode, receivedData: false) == .httpError {
            completionHandler(.cancel)
            finish(ProbeResult(status: "failed", latencyMs: nil, errorCode: "http", message: "HTTP status \(statusCode)"))
            return
        }
        completionHandler(.allow)
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        guard !data.isEmpty else { return }
        receivedData = true
        cancelledAfterFirstByte = true
        let latencyMs = Int((Date().timeIntervalSince(started ?? Date())) * 1000)
        task?.cancel()
        finish(ProbeResult(status: "healthy", latencyMs: latencyMs, errorCode: nil, message: nil))
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        if let error = error as NSError? {
            // Cancellation after receiving the first byte is our successful
            // completion path, not a transport failure.
            if cancelledAfterFirstByte && error.domain == NSURLErrorDomain && error.code == NSURLErrorCancelled { return }
            finish(ProbeResult(status: "failed", latencyMs: nil, errorCode: probeErrorCode(domain: error.domain, code: error.code), message: error.localizedDescription))
            return
        }
        let outcome = probeHTTPResponseOutcome(statusCode: statusCode ?? 0, receivedData: receivedData)
        switch outcome {
        case .healthy:
            // The first data callback normally resolves this already; retain a
            // safe fallback for URLSession implementations that complete after data.
            let latencyMs = Int((Date().timeIntervalSince(started ?? Date())) * 1000)
            finish(ProbeResult(status: "healthy", latencyMs: latencyMs, errorCode: nil, message: nil))
        case .httpError:
            finish(ProbeResult(status: "failed", latencyMs: nil, errorCode: "http", message: "HTTP status \(statusCode ?? 0)"))
        case .closedWithoutData:
            finish(ProbeResult(status: "failed", latencyMs: nil, errorCode: "network", message: "stream closed before sending audio"))
        }
    }

    private func finish(_ result: ProbeResult) {
        guard let continuation else { return }
        self.continuation = nil
        task = nil
        session?.invalidateAndCancel()
        session = nil
        continuation.resume(returning: result)
    }
}
struct StateSnapshot: Codable { let sequence: UInt64; let state: State }
struct Track: Codable, Sendable { let kind: String; let id: String?; let url: String?; let title: String; let artist: String?; let previewURL: String? }
struct ITunesSearchResponse: Decodable { let results: [ITunesSong] }
struct ITunesSong: Decodable { let trackId: Int; let trackName: String; let artistName: String; let trackViewUrl: String?; let previewUrl: String? }
enum Result: Encodable {
    case state(State), stateSnapshot(StateSnapshot), authorization(Authorization), diagnostics(TokenDiagnostics), hello(Hello), tracks([Track]), probe(ProbeResult), empty
    func encode(to encoder: Encoder) throws {
        switch self {
        case .state(let value): try value.encode(to: encoder)
        case .stateSnapshot(let value): try value.encode(to: encoder)
        case .authorization(let value): try value.encode(to: encoder)
        case .diagnostics(let value): try value.encode(to: encoder)
        case .hello(let value): try value.encode(to: encoder)
        case .tracks(let value): try value.encode(to: encoder)
        case .probe(let value): try value.encode(to: encoder)
        case .empty: var c = encoder.singleValueContainer(); try c.encode([String: String]())
        }
    }
}
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
                // Probes are read-only and may take up to their own timeout, so
                // they answer from a detached task instead of blocking the
                // serial command loop (play/pause/shutdown stay responsive).
                if request.method == "radioProbe" {
                    Task { [weak self] in
                        let response = await LiltPlayer.radioProbeResponse(request)
                        try? await self?.send(response)
                    }
                    continue
                }
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
        let snapshot = StateSnapshot(sequence: sequence, state: state)
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
        let notification = RPCNotification(params: StateSnapshot(sequence: sequence, state: state))
        guard let notificationData = try? encoded(notification) else { lock.unlock(); return }
        // Submission occurs while holding the state lock, so notification
        // sequence order and writer queue order cannot diverge.
        writerQueue.async { [weak self] in try? self?.write(notificationData) }
        lock.unlock()
        DispatchQueue.main.async { LiltPlayer.updateNowPlaying(state) }
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
        DispatchQueue.main.async { LiltPlayer.clearNowPlaying() }
    }
}

enum SocketError: LocalizedError {
    case pathTooLong, pathExists, system(String, Int32)
    var errorDescription: String? {
        switch self {
        case .pathTooLong: return "RPC socket path exceeds the macOS Unix socket limit"
        case .pathExists: return "RPC socket path already exists"
        case .system(let operation, let code): return "\(operation) failed: \(String(cString: strerror(code)))"
        }
    }
}

@main
@MainActor
final class LiltPlayer: NSObject, NSApplicationDelegate {
    private static var retainedDelegate: LiltPlayer?
    private static var previewPlayer: AVPlayer?
    private static var streamPlayer: AVPlayer?
    // AVPlayer keeps reporting waitingToPlayAtSpecifiedRate for a live stream
    // after pause, so status would stay "buffering" and hide the pause. This
    // flag carries the explicit intent until playback resumes.
    private static var streamPaused = false
    private static var streamStartedAt: Date?
    private static var currentTrack: Track?
    private static var mode = "none"
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
        MusicDataRequest.tokenProvider = FreshMusicTokenProvider()
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
        } else if server != nil {
            registerRemoteCommands()
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
        ["play", "pause", "resume", "next", "previous", "setShuffle", "setRepeat", "stop", "enqueue", "playSongs", "queueJump", "queueRemove", "queueMove", "queueClear", "radioPlay", "radioStop"].contains(method)
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
        case "recommendations": return .tracks(try await recommendations())
        case "playlistTracks": return .tracks(try await playlistTracks(request.params))
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
            if request.method == "next" { try await ApplicationMusicPlayer.shared.skipToNextEntry() }
            else { try await ApplicationMusicPlayer.shared.skipToPreviousEntry() }
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
        case "playSongs":
            try await playSongs(request.params)
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
        case "radioPlay":
            try radioPlay(request.params)
            return .state(state())
        case "radioStop":
            radioStop()
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
    static func playlistTracks(_ params: [String: JSONValue]?) async throws -> [Track] {
        guard let id = params?["id"]?.string, !id.isEmpty else { throw PlayerError.invalidReference }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        if let catalog = try? await catalogPlaylistEntries(id), !catalog.isEmpty {
            return catalog
        }
        if let library = try? await libraryPlaylistEntries(id), !library.isEmpty {
            return library
        }
        throw PlayerError.invalidReference
    }
    static func catalogPlaylistEntries(_ id: String) async throws -> [Track] {
        var request = MusicCatalogResourceRequest<Playlist>(matching: \.id, equalTo: MusicItemID(id))
        request.properties = [.entries]
        guard let playlist = try await request.response().items.first else { return [] }
        if let entries = playlist.entries { return entries.compactMap(entryTrack) }
        return []
    }
    static func libraryPlaylistEntries(_ id: String) async throws -> [Track] {
        var request = MusicLibraryRequest<Playlist>()
        request.filter(matching: \.id, equalTo: MusicItemID(id))
        guard let playlist = try await request.response().items.first else { return [] }
        if let entries = playlist.entries, !entries.isEmpty { return entries.compactMap(entryTrack) }
        if let full = try? await playlist.with([.entries]), let entries = full.entries, !entries.isEmpty {
            return entries.compactMap(entryTrack)
        }
        if let full = try? await playlist.with([.tracks]), let tracks = full.tracks, !tracks.isEmpty {
            return tracks.compactMap { track in
                guard case let .song(song) = track else { return nil }
                return songTrack(song)
            }
        }
        return []
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
        guard let url = params?["url"]?.string, !url.isEmpty, let components = URLComponents(string: url), let id = canonicalID(PlaybackRequest(kind: "", id: nil, storefront: nil, url: url, startAt: nil, startTrackID: nil, startTitle: nil, reverse: nil)) else { throw PlayerError.invalidReference }
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
        let request = PlaybackRequest(kind: kind, id: params["id"]?.string, storefront: params["storefront"]?.string, url: params["url"]?.string, startAt: params["startAt"]?.int, startTrackID: params["startTrackID"]?.string, startTitle: params["startTitle"]?.string, reverse: params["reverse"]?.bool)
        guard ["song", "playlist", "station"].contains(request.kind), let id = canonicalID(request) else { throw PlayerError.invalidReference }
        streamPlayer?.pause()
        streamPlayer = nil
        clearAVObservation()
        playbackError = nil
        if mode == "stream" {
            // Leaving a stream: end live state before validating the new source
            // so a failed start cannot report the old station as still playing.
            currentTrack = nil
            mode = "none"
            streamPaused = false
            streamStartedAt = nil
        }
        if authorizationStatus() != "authorized" {
            guard request.kind == "song" else { throw PlayerError.authorizationRequired }
            try await playPreview(id: id)
            return
        }
        do {
            if request.kind == "playlist" {
                var songs = try await playlistSongs(id)
                if request.reverse == true { songs.reverse() }
                guard !songs.isEmpty else { throw PlayerError.invalidReference }
                let descriptors = songs.map { StartTrack(id: $0.id.rawValue, title: $0.title) }
                let startIndex = selectedStartIndex(tracks: descriptors, id: request.startTrackID, index: request.startAt, title: request.startTitle)
                currentTrack = songTrack(songs[startIndex])
                previewPlayer?.pause(); mode = "full"
                let player = ApplicationMusicPlayer.shared
                let entries = songs.map { ApplicationMusicPlayer.Queue.Entry($0) }
                player.queue = .init(entries, startingAt: entries[startIndex])
            } else if request.kind == "station" {
                let catalog = MusicCatalogResourceRequest<Station>(matching: \.id, equalTo: MusicItemID(id))
                guard let station = try await catalog.response().items.first else { throw PlayerError.invalidReference }
                currentTrack = Track(kind: "station", id: station.id.rawValue, url: station.url?.absoluteString, title: station.name, artist: station.stationProviderName, previewURL: nil)
                previewPlayer?.pause(); mode = "full"
                ApplicationMusicPlayer.shared.queue = .init(for: [station])
            } else {
                var song = try? await catalogSong(id)
                if song == nil { song = try? await librarySong(id) }
                guard let song else { throw PlayerError.invalidReference }
                currentTrack = Track(kind: "song", id: song.id.rawValue, url: song.url?.absoluteString, title: song.title, artist: song.artistName, previewURL: song.previewAssets?.first?.url?.absoluteString)
                previewPlayer?.pause(); mode = "full"
                ApplicationMusicPlayer.shared.queue = .init(for: [song])
            }
            try await ApplicationMusicPlayer.shared.play()
        } catch {
            guard request.kind == "song" else { throw error }
            stopMusicAndWaitForSilence()
            fputs("MusicKit full playback unavailable; using preview: \(errorDetails(error))\n", stderr)
            try await playPreview(id: id)
        }
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
        let request = PlaybackRequest(kind: kind, id: params["id"]?.string, storefront: params["storefront"]?.string, url: params["url"]?.string, startAt: nil, startTrackID: nil, startTitle: nil, reverse: nil)
        guard ["song", "playlist", "station"].contains(request.kind), let id = canonicalID(request) else { throw PlayerError.invalidReference }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        guard !ApplicationMusicPlayer.shared.queue.entries.isEmpty else { throw PlayerError.queueUnavailable }
        let position: MusicKit.MusicPlayer.Queue.EntryInsertionPosition = params["position"]?.string == "next" ? .afterCurrentEntry : .tail
        if request.kind == "playlist" {
            var library = MusicLibraryRequest<Playlist>()
            library.filter(matching: \.id, equalTo: MusicItemID(id))
            if let playlist = try await library.response().items.first {
                try await ApplicationMusicPlayer.shared.queue.insert(playlist, position: position)
                return
            }
            let catalog = MusicCatalogResourceRequest<Playlist>(matching: \.id, equalTo: MusicItemID(id))
            guard let playlist = try await catalog.response().items.first else { throw PlayerError.invalidReference }
            try await ApplicationMusicPlayer.shared.queue.insert(playlist, position: position)
        } else if request.kind == "station" {
            let catalog = MusicCatalogResourceRequest<Station>(matching: \.id, equalTo: MusicItemID(id))
            guard let station = try await catalog.response().items.first else { throw PlayerError.invalidReference }
            try await ApplicationMusicPlayer.shared.queue.insert(station, position: position)
        } else {
            var song = try? await catalogSong(id)
            if song == nil { song = try? await librarySong(id) }
            guard let song else { throw PlayerError.invalidReference }
            try await ApplicationMusicPlayer.shared.queue.insert(song, position: position)
        }
    }
    static func stop() {
        previewPlayer?.pause()
        streamPlayer?.pause()
        ApplicationMusicPlayer.shared.stop()
        clearAVObservation()
        mode = "none"
        currentTrack = nil
        previewPlayer = nil
        streamPlayer = nil
        playbackError = nil
        ApplicationMusicPlayer.shared.queue.entries = .init()
    }
    static func playSongs(_ params: [String: JSONValue]?) async throws {
        guard let values = params?["ids"]?.array, !values.isEmpty else { throw PlayerError.invalidReference }
        guard authorizationStatus() == "authorized" else { throw PlayerError.authorizationRequired }
        var songs: [Song] = []
        for value in values {
            guard let id = value.string, !id.isEmpty else { continue }
            var song = try? await catalogSong(id)
            if song == nil { song = try? await librarySong(id) }
            if let song { songs.append(song) }
        }
        guard !songs.isEmpty else { throw PlayerError.invalidReference }
        let startIndex = max(0, min(params?["startIndex"]?.int ?? 0, songs.count - 1))
        previewPlayer?.pause(); streamPlayer?.pause(); streamPlayer = nil
        clearAVObservation()
        currentTrack = Track(kind: "song", id: songs[startIndex].id.rawValue, url: songs[startIndex].url?.absoluteString, title: songs[startIndex].title, artist: songs[startIndex].artistName, previewURL: songs[startIndex].previewAssets?.first?.url?.absoluteString)
        mode = "full"
        let entries = songs.map { ApplicationMusicPlayer.Queue.Entry($0) }
        ApplicationMusicPlayer.shared.queue = .init(entries, startingAt: entries[startIndex])
        try await ApplicationMusicPlayer.shared.play()
    }
    // queueJump starts playback at the chosen queue entry. MusicKit rejects a
    // start item it cannot match inside a larger queue with "Prepare queue
    // failed with unexpected start item" (Code 6), which happens for some
    // library and already-played entries. Three strategies are tried in order:
    //
    //   1. rebuild the full queue with the target as the start item (keeps the
    //      history so the user can still jump back),
    //   2. rebuild from the target onward (drops history, keeps the track),
    //   3. step the existing queue to the target without rebuilding it.
    //
    // Helper stderr is not captured (LaunchServices launch), so a total failure
    // throws a descriptive error that the TUI shows and logs.
    static func queueJump(_ params: [String: JSONValue]?) async throws {
        guard mode == "full" else { throw PlayerError.previewUnsupported }
        let player = ApplicationMusicPlayer.shared
        let entries = Array(player.queue.entries)
        guard let index = params?["index"]?.int, entries.indices.contains(index) else { throw PlayerError.invalidReference }

        if await rebuild(player, entries: entries, start: index) { return }
        let remaining = Array(entries[index...])
        if !remaining.isEmpty, await rebuild(player, entries: remaining, start: 0) { return }
        if await step(player, to: index) { return }

        throw NSError(domain: "lilt", code: 1, userInfo: [
            NSLocalizedDescriptionKey: "queue jump to entry \(index) of \(entries.count) failed: MusicKit rejected the queue rebuild and stepping could not reach the entry",
        ])
    }
    // rebuild replaces the queue and reports whether playback started.
    private static func rebuild(_ player: ApplicationMusicPlayer, entries: [ApplicationMusicPlayer.Queue.Entry], start: Int) async -> Bool {
        guard entries.indices.contains(start) else { return false }
        player.queue = .init(entries, startingAt: entries[start])
        do {
            try await player.play()
            return true
        } catch {
            return false
        }
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
        var entries = player.queue.entries
        guard let index = params?["index"]?.int, entries.indices.contains(index) else { return }
        entries.remove(at: index)
        player.queue.entries = entries
    }
    static func queueMove(_ params: [String: JSONValue]?) {
        guard mode == "full" else { return }
        let player = ApplicationMusicPlayer.shared
        var entries = player.queue.entries
        guard let from = params?["from"]?.int, let to = params?["to"]?.int, entries.indices.contains(from), to >= 0, to < entries.count else { return }
        let entry = entries.remove(at: from)
        entries.insert(entry, at: to)
        player.queue.entries = entries
    }
    static func queueClear() {
        previewPlayer?.pause()
        streamPlayer?.pause()
        ApplicationMusicPlayer.shared.stop()
        ApplicationMusicPlayer.shared.queue.entries = .init()
        mode = "none"
        currentTrack = nil
        playbackError = nil
    }
    static func canonicalID(_ request: PlaybackRequest) -> String? {
        if let id = request.id, !id.isEmpty { return id.contains(":") ? String(id.split(separator: ":", maxSplits: 1)[1]) : id }
        guard let url = request.url, let components = URLComponents(string: url), components.host?.hasSuffix("music.apple.com") == true else { return nil }
        return components.queryItems?.first(where: { $0.name == "i" })?.value ?? components.path.split(separator: "/").last.map(String.init)
    }
    static func pause() {
        if mode == "full" { ApplicationMusicPlayer.shared.pause() }
        else if mode == "stream" { streamPaused = true; streamPlayer?.pause() }
        else { previewPlayer?.pause() }
    }
    static func resume() async throws {
        if mode == "full" { try await ApplicationMusicPlayer.shared.play() }
        else if mode == "stream" { guard let streamPlayer else { throw PlayerError.previewUnavailable }; streamPaused = false; playbackError = nil; streamStartedAt = Date(); streamPlayer.isMuted = false; streamPlayer.volume = 1; streamPlayer.play() }
        else if let previewPlayer { previewPlayer.play() }
        else { throw PlayerError.nothingPlaying }
    }
    static func stopPlayback() {
        previewPlayer?.pause()
        streamPlayer?.pause()
        streamPaused = false
        streamStartedAt = nil
        ApplicationMusicPlayer.shared.pause()
        clearAVObservation()
        mode = "none"
        currentTrack = nil
        previewPlayer = nil
        streamPlayer = nil
        playbackError = nil
        ApplicationMusicPlayer.shared.queue.entries = .init()
    }
    static func publishCurrentState() {
        statePublisher?.publish(state())
    }

    static func updateNowPlaying(_ state: State) {
        let center = MPNowPlayingInfoCenter.default()
        guard let track = state.track, state.mode != "none", state.status != "stopped" else {
            center.nowPlayingInfo = nil
            center.playbackState = .stopped
            return
        }
        var info: [String: Any] = [
            MPMediaItemPropertyTitle: track.title,
            MPMediaItemPropertyArtist: track.artist ?? "",
            MPNowPlayingInfoPropertyElapsedPlaybackTime: state.position,
            MPNowPlayingInfoPropertyPlaybackRate: state.status == "paused" ? 0.0 : 1.0,
            MPNowPlayingInfoPropertyMediaType: MPNowPlayingInfoMediaType.audio.rawValue,
        ]
        if state.isLive {
            info[MPNowPlayingInfoPropertyIsLiveStream] = true
            info[MPMediaItemPropertyPlaybackDuration] = 0.0
        } else {
            info[MPMediaItemPropertyPlaybackDuration] = state.duration
        }
        center.nowPlayingInfo = info
        switch state.status {
        case "playing", "buffering":
            center.playbackState = .playing
        case "paused", "error":
            center.playbackState = .paused
        default:
            center.playbackState = .stopped
        }
    }

    static func clearNowPlaying() {
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
        MPNowPlayingInfoCenter.default().playbackState = .stopped
    }

    private func registerRemoteCommands() {
        let center = MPRemoteCommandCenter.shared()
        center.playCommand.addTarget { _ in
            Task { @MainActor in _ = try? await Self.resume(); Self.publishCurrentState() }
            return .success
        }
        center.pauseCommand.addTarget { _ in
            Task { @MainActor in Self.pause(); Self.publishCurrentState() }
            return .success
        }
        center.togglePlayPauseCommand.addTarget { _ in
            Task { @MainActor in
                switch Self.state().status {
                case "playing", "buffering": Self.pause()
                default: _ = try? await Self.resume()
                }
                Self.publishCurrentState()
            }
            return .success
        }
        center.stopCommand.addTarget { _ in
            Task { @MainActor in Self.stopPlayback(); Self.publishCurrentState() }
            return .success
        }
        center.nextTrackCommand.addTarget { _ in
            Task { @MainActor in
                if Self.mode == "full" { try? await ApplicationMusicPlayer.shared.skipToNextEntry() }
                Self.publishCurrentState()
            }
            return .success
        }
        center.previousTrackCommand.addTarget { _ in
            Task { @MainActor in
                if Self.mode == "full" { try? await ApplicationMusicPlayer.shared.skipToPreviousEntry() }
                Self.publishCurrentState()
            }
            return .success
        }
    }

    static func state() -> State {
        if mode == "full" {
            let player = ApplicationMusicPlayer.shared
            let current = player.queue.currentEntry
            var queue: [Track] = []
            var index = 0
            for (offset, entry) in player.queue.entries.enumerated() {
                if entry.id == current?.id { index = offset }
                queue.append(queueTrack(entry))
            }
            let track = current.map(queueTrack) ?? currentTrack
            return State(track: track, position: player.playbackTime, duration: duration(of: current) ?? 0, status: playbackError == nil ? fullPlaybackStatus(player) : "error", audioVariant: player.state.audioVariant.map { String(describing: $0) }, format: formatLabel(player.state.audioVariant), availableFormats: availableFormats(for: track?.id), shuffle: player.state.shuffleMode == .songs, repeatMode: repeatLabel(player.state.repeatMode), isLive: false, mode: mode, authorization: authorizationStatus(), accountStatus: accountStatus, accountError: accountError, playbackError: playbackError, queue: queue, queueIndex: index)
        }
        if mode == "stream" {
            let seconds = streamPlayer?.currentTime().seconds ?? 0
            let streamPlaying = streamPlayer?.timeControlStatus == .playing
            // The 10s guard covers one start attempt. Disarm it once audio is
            // flowing so a later pause/resume or stall cannot be judged against
            // the original start time, and re-arm a fresh window for a stall
            // that happens after playback began.
            if streamPlaying {
                streamStartedAt = nil
            } else if !streamPaused, playbackError == nil, streamStartedAt == nil {
                streamStartedAt = Date()
            }
            if let startedAt = streamStartedAt, playbackError == nil, !streamPaused,
               !streamPlaying,
               Date().timeIntervalSince(startedAt) > 10 {
                playbackError = "Stream did not start within 10s — press v to stop, or Enter/p to retry"
            }
            let status: String
            if streamPaused {
                status = "paused"
            } else {
                switch streamPlayer?.timeControlStatus {
                case .playing: status = "playing"
                case .waitingToPlayAtSpecifiedRate: status = "buffering"
                default: status = "paused"
                }
            }
            return State(track: currentTrack, position: seconds.isFinite ? seconds : 0, duration: 0, status: playbackError == nil ? status : "error", audioVariant: nil, format: "live stream", availableFormats: [], shuffle: false, repeatMode: "off", isLive: true, mode: mode, authorization: authorizationStatus(), accountStatus: accountStatus, accountError: accountError, playbackError: playbackError, queue: [], queueIndex: 0)
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

    // MusicKit's stop() keeps sounding for up to ~3s on this platform. A stream
    // that starts meanwhile would be audible alongside Apple Music, so it starts
    // muted and is unmuted only once MusicKit is actually silent.
    static func unmuteWhenMusicSilent(_ player: AVPlayer, timeout: TimeInterval = 4) {
        Task { @MainActor in
            let deadline = Date().addingTimeInterval(timeout)
            while Date() < deadline, musicIsPlaying() {
                try? await Task.sleep(nanoseconds: 50_000_000)
            }
            guard streamPlayer === player, mode == "stream", !streamPaused else {
                player.pause()
                return
            }
            player.isMuted = false
            player.volume = 1
        }
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

    static func radioPlay(_ params: [String: JSONValue]?) throws {
        guard let urlString = params?["url"]?.string, let url = URL(string: urlString) else { throw PlayerError.invalidReference }
        let musicWasPlaying = musicIsPlaying()
        ApplicationMusicPlayer.shared.stop()
        previewPlayer?.pause(); previewPlayer = nil
        streamPlayer?.pause()
        playbackError = nil
        streamPaused = false
        streamStartedAt = Date()
        currentTrack = Track(kind: "stream", id: nil, url: urlString, title: params?["name"]?.string ?? urlString, artist: nil, previewURL: nil)
        mode = "stream"
        let player = AVPlayer(url: url)
        if musicWasPlaying {
            player.isMuted = true
            player.volume = 0
        }
        streamPlayer = player
        observe(player)
        player.play()
        if musicWasPlaying { unmuteWhenMusicSilent(player) }
    }
    static func radioStop() {
        streamPlayer?.pause()
        streamPaused = false
        streamStartedAt = nil
        clearAVObservation()
        streamPlayer = nil
        mode = "none"
        currentTrack = nil
        playbackError = nil
    }

    // radioProbeResponse runs one read-only reachability probe. It never
    // touches the playback-owned players or publishes state.
    nonisolated static func radioProbeResponse(_ request: RPCRequest) async -> RPCResponse {
        let url = request.params?["url"]?.string ?? ""
        let timeoutMs = request.params?["timeoutMs"]?.int ?? 6000
        let probed = await probeStream(url: url, timeoutMs: timeoutMs)
        return RPCResponse(id: request.id, result: .probe(probed), error: nil)
    }

    // probeStream measures HTTP time to first audio byte without touching any
    // playback-owned AVFoundation objects.
    static func probeStream(url: String, timeoutMs: Int) async -> ProbeResult {
        let trimmed = url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let parsed = URL(string: trimmed),
              let scheme = parsed.scheme?.lowercased(),
              scheme == "http" || scheme == "https" else {
            return ProbeResult(status: "failed", latencyMs: nil, errorCode: "unsupported", message: "only http and https streams can be probed")
        }
        let timeout = min(max(timeoutMs, 500), 15000)
        var request = URLRequest(url: parsed, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: Double(timeout) / 1000)
        request.httpMethod = "GET"
        request.setValue("lilt-player/1.0 (macOS; radio health probe)", forHTTPHeaderField: "User-Agent")
        request.setValue("*/*", forHTTPHeaderField: "Accept")
        let probe = HTTPFirstByteProbe()
        return await probe.run(request: request)
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
        streamPlayer?.pause()
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
        case .unknownMethod: return "unknown_method"
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
