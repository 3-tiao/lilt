import AppKit
import AVFoundation
import Darwin
import Foundation
import MediaPlayer

public struct JSONValue: Codable, Sendable {
    private let storage: Storage
    private enum Storage: Sendable { case string(String), int(Int), bool(Bool), object([String: JSONValue]), array([JSONValue]), null }
    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { storage = .null }
        else if let value = try? c.decode(Bool.self) { storage = .bool(value) }
        else if let value = try? c.decode(Int.self) { storage = .int(value) }
        else if let value = try? c.decode(String.self) { storage = .string(value) }
        else if let value = try? c.decode([JSONValue].self) { storage = .array(value) }
        else { storage = .object(try c.decode([String: JSONValue].self)) }
    }
    public func encode(to encoder: Encoder) throws {
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
    public var string: String? { if case .string(let value) = storage { return value }; return nil }
    public var int: Int? { if case .int(let value) = storage { return value }; return nil }
    public var bool: Bool? { if case .bool(let value) = storage { return value }; return nil }
    public var array: [JSONValue]? { if case .array(let value) = storage { return value }; return nil }
}

public struct RPCRequest: Codable, Sendable {
    public let jsonrpc: String
    public let id: Int
    public let method: String
    public let params: [String: JSONValue]?
}
public struct RPCError: Codable, Sendable { public let code: String; public let message: String
    public init(code: String, message: String) { self.code = code; self.message = message } }
public struct HelperTrack: Codable, Sendable {
    public let kind: String; public let id: String?; public let url: String?; public let title: String; public let artist: String?; public let previewURL: String?
    public init(kind: String, id: String?, url: String?, title: String, artist: String?, previewURL: String?) {
        self.kind = kind; self.id = id; self.url = url; self.title = title; self.artist = artist; self.previewURL = previewURL
    }
}
public struct State: Codable, Sendable {
    public let track: HelperTrack?; public let position: Double; public let duration: Double; public let status: String; public let audioVariant: String?; public let format: String; public let availableFormats: [String]; public let shuffle: Bool; public let repeatMode: String; public let isLive: Bool; public let mode: String; public let authorization: String; public let accountStatus: String?; public let accountError: String?; public let playbackError: String?; public let queue: [HelperTrack]; public let queueIndex: Int; public let ended: Bool?; public let playbackGeneration: UInt64?; public let transportSessionID: String?
    public init(track: HelperTrack?, position: Double, duration: Double, status: String, audioVariant: String?, format: String, availableFormats: [String], shuffle: Bool, repeatMode: String, isLive: Bool, mode: String, authorization: String, accountStatus: String?, accountError: String?, playbackError: String?, queue: [HelperTrack], queueIndex: Int, ended: Bool? = nil, playbackGeneration: UInt64? = nil, transportSessionID: String? = nil) {
        self.track = track; self.position = position; self.duration = duration; self.status = status; self.audioVariant = audioVariant; self.format = format; self.availableFormats = availableFormats; self.shuffle = shuffle; self.repeatMode = repeatMode; self.isLive = isLive; self.mode = mode; self.authorization = authorization; self.accountStatus = accountStatus; self.accountError = accountError; self.playbackError = playbackError; self.queue = queue; self.queueIndex = queueIndex; self.ended = ended; self.playbackGeneration = playbackGeneration; self.transportSessionID = transportSessionID
    }
}
public struct ProbeResult: Codable, Sendable { public let status: String; public let latencyMs: Int?; public let errorCode: String?; public let message: String?
    public init(status: String, latencyMs: Int?, errorCode: String?, message: String?) { self.status = status; self.latencyMs = latencyMs; self.errorCode = errorCode; self.message = message } }
public struct StateSnapshot: Codable, Sendable { public let sequence: UInt64; public let state: State; public let playbackGeneration: UInt64?; public let transportSessionID: String?
    public init(sequence: UInt64, state: State, playbackGeneration: UInt64?, transportSessionID: String?) { self.sequence = sequence; self.state = state; self.playbackGeneration = playbackGeneration; self.transportSessionID = transportSessionID } }
public struct Hello: Codable, Sendable { public let pid: Int32
    public init(pid: Int32) { self.pid = pid } }

public enum RPCResult: Encodable, Sendable {
    case state(State), snapshot(StateSnapshot), hello(Hello), probe(ProbeResult), empty
    public func encode(to encoder: Encoder) throws {
        switch self {
        case .state(let value): try value.encode(to: encoder)
        case .snapshot(let value): try value.encode(to: encoder)
        case .hello(let value): try value.encode(to: encoder)
        case .probe(let value): try value.encode(to: encoder)
        case .empty: var c = encoder.singleValueContainer(); try c.encode([String: String]())
        }
    }
}
public struct RPCResponse: Encodable, Sendable { public let jsonrpc = "2.0"; public let id: Int; public let result: RPCResult?; public let error: RPCError? }
private struct RPCNotification: Encodable { let jsonrpc = "2.0"; let method = "stateChanged"; let params: StateSnapshot }

public enum SocketError: LocalizedError {
    case pathTooLong, pathExists, system(String, Int32)
    public var errorDescription: String? {
        switch self {
        case .pathTooLong: return "RPC socket path exceeds the macOS Unix socket limit"
        case .pathExists: return "RPC socket path already exists"
        case .system(let operation, let code): return "\(operation) failed: \(String(cString: strerror(code)))"
        }
    }
}

public final class RPCSocketServer: @unchecked Sendable {
    private let path: String
    private let service: AudioService
    private let lock = NSLock()
    private let writerQueue = DispatchQueue(label: "com.caiguo.lilt-audio.rpc-writer")
    private var listener: Int32 = -1
    private var connection: Int32 = -1
    private var stopped = false
    private var subscribed = false
    private var sequence: UInt64 = 0
    private var lastState: Data?

    public init(path: String, service: AudioService) { self.path = path; self.service = service }

    public func start() throws {
        guard path.utf8.count < MemoryLayout.size(ofValue: sockaddr_un().sun_path) else { throw SocketError.pathTooLong }
        guard !FileManager.default.fileExists(atPath: path) else { throw SocketError.pathExists }
        let fd = Darwin.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw SocketError.system("socket", errno) }
        var address = sockaddr_un(); address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size); address.sun_family = sa_family_t(AF_UNIX)
        let capacity = MemoryLayout.size(ofValue: address.sun_path)
        path.withCString { source in withUnsafeMutablePointer(to: &address.sun_path) { tuple in tuple.withMemoryRebound(to: CChar.self, capacity: capacity) { _ = strncpy($0, source, capacity - 1) } } }
        let bound = withUnsafePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
        guard bound == 0 else { let code = errno; Darwin.close(fd); throw SocketError.system("bind", code) }
        guard Darwin.chmod(path, mode_t(S_IRUSR | S_IWUSR)) == 0, Darwin.listen(fd, 1) == 0 else { let code = errno; Darwin.close(fd); Darwin.unlink(path); throw SocketError.system("listen", code) }
        lock.lock(); listener = fd; lock.unlock()
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in self?.acceptHost() }
        DispatchQueue.global().asyncAfter(deadline: .now() + 15) { [weak self] in
            guard let self else { return }; self.lock.lock(); let abandoned = self.connection < 0 && !self.stopped; self.lock.unlock()
            if abandoned { DispatchQueue.main.async { NSApplication.shared.terminate(nil) } }
        }
    }

    private func acceptHost() {
        lock.lock(); let fd = listener; lock.unlock()
        let accepted = Darwin.accept(fd, nil, nil); guard accepted >= 0 else { return }
        var noSigPipe: Int32 = 1; _ = setsockopt(accepted, SOL_SOCKET, SO_NOSIGPIPE, &noSigPipe, socklen_t(MemoryLayout<Int32>.size))
        lock.lock(); if stopped { lock.unlock(); Darwin.close(accepted); return }; connection = accepted; lock.unlock()
        Task { await process(accepted) }
    }

    private func process(_ fd: Int32) async {
        let file = FileHandle(fileDescriptor: fd, closeOnDealloc: false)
        do {
            for try await line in file.bytes.lines {
                guard let request = try? JSONDecoder().decode(RPCRequest.self, from: Data(line.utf8)), request.jsonrpc == "2.0" else { continue }
                if request.method == "radioProbe" {
                    Task { [weak self] in if let response = await self?.service.handle(request) { try? await self?.send(response.0) } }
                    continue
                }
                let response: RPCResponse; let shutdown: Bool
                if request.method == "subscribeState" {
                    let state = await service.state(); response = RPCResponse(id: request.id, result: .snapshot(subscribe(state)), error: nil); shutdown = false
                } else if request.method == "unsubscribeState" {
                    unsubscribe(); response = RPCResponse(id: request.id, result: .empty, error: nil); shutdown = false
                } else {
                    (response, shutdown) = await service.handle(request)
                }
                try await send(response)
                let stateChanging = await service.isStateChanging(request.method)
                if response.error == nil && stateChanging { publish(await service.state()) }
                if shutdown { break }
            }
        } catch { fputs("lilt-audio RPC connection ended: \(error.localizedDescription)\n", stderr) }
        stop(); await service.stop(); await MainActor.run { NSApplication.shared.terminate(nil) }
    }

    private func encoded<T: Encodable>(_ value: T) throws -> Data { let encoder = JSONEncoder(); encoder.outputFormatting = [.sortedKeys]; var data = try encoder.encode(value); data.append(0x0a); return data }
    private func send<T: Encodable>(_ value: T) async throws {
        let data = try encoded(value)
        try await withCheckedThrowingContinuation { continuation in writerQueue.async { [weak self] in do { try self?.write(data); continuation.resume() } catch { continuation.resume(throwing: error) } } }
    }
    private func write(_ data: Data) throws {
        lock.lock(); let fd = stopped ? -1 : connection; lock.unlock(); guard fd >= 0 else { throw CocoaError(.fileNoSuchFile) }
        try data.withUnsafeBytes { bytes in guard let base = bytes.baseAddress else { return }; var offset = 0; while offset < bytes.count { let count = Darwin.write(fd, base.advanced(by: offset), bytes.count - offset); if count < 0 && errno == EINTR { continue }; guard count > 0 else { throw SocketError.system("write", errno) }; offset += count } }
    }
    private func subscribe(_ state: State) -> StateSnapshot { lock.lock(); subscribed = true; lastState = try? JSONEncoder().encode(state); let value = StateSnapshot(sequence: sequence, state: state, playbackGeneration: state.playbackGeneration, transportSessionID: state.transportSessionID); lock.unlock(); return value }
    private func unsubscribe() { lock.lock(); subscribed = false; lock.unlock() }
    public func publish(_ state: State) {
        lock.lock(); let data = try? JSONEncoder().encode(state); guard subscribed, data != lastState else { lock.unlock(); return }; sequence &+= 1; lastState = data
        let note = RPCNotification(params: StateSnapshot(sequence: sequence, state: state, playbackGeneration: state.playbackGeneration, transportSessionID: state.transportSessionID)); guard let bytes = try? encoded(note) else { lock.unlock(); return }; writerQueue.async { [weak self] in try? self?.write(bytes) }; lock.unlock()
        Task { @MainActor in self.service.updateNowPlaying(state) }
    }
    public func stop() {
        lock.lock(); if stopped { lock.unlock(); return }; stopped = true; let l = listener; let c = connection; listener = -1; connection = -1; lock.unlock()
        if c >= 0 { writerQueue.sync { Darwin.shutdown(c, SHUT_RDWR); Darwin.close(c) } }; if l >= 0 { Darwin.shutdown(l, SHUT_RDWR); Darwin.close(l) }; Darwin.unlink(path)
        Task { @MainActor in service.clearNowPlaying() }
    }
}

private final class HTTPProbe: NSObject, URLSessionDataDelegate, @unchecked Sendable {
    private var continuation: CheckedContinuation<ProbeResult, Never>?; private var session: URLSession?; private var task: URLSessionDataTask?; private var started = Date(); private var status = 0
    func run(_ request: URLRequest) async -> ProbeResult { await withCheckedContinuation { continuation = $0; let config = URLSessionConfiguration.ephemeral; config.urlCache = nil; config.timeoutIntervalForRequest = request.timeoutInterval; session = URLSession(configuration: config, delegate: self, delegateQueue: nil); task = session!.dataTask(with: request); started = Date(); task!.resume() } }
    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive response: URLResponse, completionHandler: @escaping (URLSession.ResponseDisposition) -> Void) { status = (response as? HTTPURLResponse)?.statusCode ?? 0; if !(200..<400).contains(status) { completionHandler(.cancel); finish(.init(status: "failed", latencyMs: nil, errorCode: "http", message: "HTTP status \(status)")) } else { completionHandler(.allow) } }
    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) { guard !data.isEmpty else { return }; finish(.init(status: "healthy", latencyMs: Int(Date().timeIntervalSince(started) * 1000), errorCode: nil, message: nil)); task?.cancel() }
    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) { if continuation == nil { return }; if let error = error as NSError? { let code = error.code == NSURLErrorTimedOut ? "timeout" : "network"; finish(.init(status: "failed", latencyMs: nil, errorCode: code, message: error.localizedDescription)) } else { finish(.init(status: "failed", latencyMs: nil, errorCode: "network", message: "stream closed before sending audio")) } }
    private func finish(_ result: ProbeResult) { guard let continuation else { return }; self.continuation = nil; session?.invalidateAndCancel(); session = nil; continuation.resume(returning: result) }
}

@MainActor public final class AudioService {
    private var player: AVPlayer?; private var track: HelperTrack?; private var mode = "none"; private var paused = false; private var ended = false; private var duration = 0; private var generation: UInt64?; private var sessionID: String?; private var endObserver: NSObjectProtocol?; private var timeObserver: Any?; private weak var publisher: RPCSocketServer?; private var artwork: [URL: NSImage] = [:]; private var currentArtwork: NSImage?
    public init() { registerRemoteCommands() }
    public func connect(_ publisher: RPCSocketServer) { self.publisher = publisher }
    public func isStateChanging(_ method: String) -> Bool { ["urlPlay", "urlStop", "radioPlay", "radioStop", "pause", "resume", "stop"].contains(method) }
    public func handle(_ request: RPCRequest) async -> (RPCResponse, Bool) {
        do {
            let result: RPCResult
            switch request.method {
            case "ping": result = .hello(Hello(pid: getpid()))
            case "urlPlay": try await playURL(request.params); result = .state(state())
            case "urlStop": stop(); result = .state(state())
            case "radioPlay": try playRadio(request.params); result = .state(state())
            case "radioStop", "stop": stop(); result = .state(state())
            case "pause": pause(); result = .state(state())
            case "resume": try resume(); result = .state(state())
            case "state": result = .state(state())
            case "radioProbe": result = .probe(await probe(request.params))
            case "shutdown": stop(); result = .empty
            default: throw AudioError.unknownCommand
            }
            return (RPCResponse(id: request.id, result: result, error: nil), request.method == "shutdown")
        } catch let error as AudioError {
            return (RPCResponse(id: request.id, result: nil, error: RPCError(code: error.code, message: error.localizedDescription)), false)
        } catch {
            return (RPCResponse(id: request.id, result: nil, error: RPCError(code: "audio_error", message: error.localizedDescription)), false)
        }
    }
    private func playURL(_ params: [String: JSONValue]?) async throws {
        guard let raw = params?["url"]?.string, let url = URL(string: raw), let title = params?["title"]?.string, let generationValue = params?["playbackGeneration"]?.int, let session = params?["transportSessionID"]?.string, !session.isEmpty else { throw AudioError.invalidReference }
        stop(); mode = "url"; duration = params?["duration"]?.int ?? 0; generation = UInt64(generationValue); sessionID = session; track = HelperTrack(kind: "song", id: params?["providerID"]?.string, url: nil, title: title, artist: params?["artist"]?.string, previewURL: nil)
        if let rawArtwork = params?["artworkURL"]?.string, let artworkURL = URL(string: rawArtwork) { currentArtwork = await loadArtwork(artworkURL) }
        startPlayer(url)
        let capturedGeneration = generation; let capturedSession = sessionID
        endObserver = NotificationCenter.default.addObserver(forName: .AVPlayerItemDidPlayToEndTime, object: player?.currentItem, queue: .main) { [weak self] _ in Task { @MainActor in guard let self, self.generation == capturedGeneration, self.sessionID == capturedSession else { return }; self.ended = true; self.player?.pause(); self.publish() } }
    }
    private func playRadio(_ params: [String: JSONValue]?) throws { guard let raw = params?["url"]?.string, let url = URL(string: raw) else { throw AudioError.invalidReference }; stop(); mode = "stream"; track = HelperTrack(kind: "stream", id: nil, url: raw, title: params?["name"]?.string ?? raw, artist: nil, previewURL: nil); startPlayer(url) }
    private func startPlayer(_ url: URL) { let next = AVPlayer(url: url); player = next; timeObserver = next.addPeriodicTimeObserver(forInterval: CMTime(seconds: 1, preferredTimescale: 10), queue: .main) { [weak self] _ in Task { @MainActor in self?.publish() } }; next.play(); paused = false }
    public func pause() { paused = true; player?.pause(); publish() }
    public func resume() throws { guard let player else { throw AudioError.nothingPlaying }; paused = false; ended = false; player.play(); publish() }
    public func stop() { if let observer = timeObserver { player?.removeTimeObserver(observer) }; timeObserver = nil; if let endObserver { NotificationCenter.default.removeObserver(endObserver) }; endObserver = nil; player?.pause(); player = nil; track = nil; mode = "none"; paused = false; ended = false; duration = 0; generation = nil; sessionID = nil; currentArtwork = nil; clearNowPlaying() }
    public func state() -> State { let seconds = player?.currentTime().seconds ?? 0; let status: String; if mode == "none" || ended { status = "stopped" } else if paused { status = "paused" } else { switch player?.timeControlStatus { case .playing: status = "playing"; case .waitingToPlayAtSpecifiedRate: status = "buffering"; default: status = "paused" } }; return State(track: track, position: seconds.isFinite ? seconds : 0, duration: Double(duration), status: status, audioVariant: nil, format: mode == "stream" ? "live stream" : "System-selected", availableFormats: [], shuffle: false, repeatMode: "off", isLive: mode == "stream", mode: mode, authorization: "not_applicable", accountStatus: nil, accountError: nil, playbackError: nil, queue: [], queueIndex: 0, ended: ended ? true : nil, playbackGeneration: generation, transportSessionID: sessionID) }
    private func publish() { let value = state(); updateNowPlaying(value); publisher?.publish(value) }
    private func loadArtwork(_ url: URL) async -> NSImage? { if let cached = artwork[url] { return cached }; guard let (data, response) = try? await URLSession.shared.data(from: url), (response as? HTTPURLResponse)?.statusCode == 200, let image = NSImage(data: data) else { return nil }; artwork[url] = image; return image }
    private func probe(_ params: [String: JSONValue]?) async -> ProbeResult { let raw = params?["url"]?.string ?? ""; guard let url = URL(string: raw), ["http", "https"].contains(url.scheme?.lowercased() ?? "") else { return ProbeResult(status: "failed", latencyMs: nil, errorCode: "unsupported", message: "only http and https streams can be probed") }; let timeout = min(max(params?["timeoutMs"]?.int ?? 6000, 500), 15000); var request = URLRequest(url: url, timeoutInterval: Double(timeout) / 1000); request.setValue("lilt-audio/1.0", forHTTPHeaderField: "User-Agent"); return await HTTPProbe().run(request) }
    public func updateNowPlaying(_ state: State) { let center = MPNowPlayingInfoCenter.default(); guard let track = state.track, state.mode != "none", state.status != "stopped" else { clearNowPlaying(); return }; var info: [String: Any] = [MPMediaItemPropertyTitle: track.title, MPMediaItemPropertyArtist: track.artist ?? "", MPNowPlayingInfoPropertyElapsedPlaybackTime: state.position, MPNowPlayingInfoPropertyPlaybackRate: state.status == "paused" ? 0.0 : 1.0, MPNowPlayingInfoPropertyMediaType: MPNowPlayingInfoMediaType.audio.rawValue]; if state.isLive { info[MPNowPlayingInfoPropertyIsLiveStream] = true } else { info[MPMediaItemPropertyPlaybackDuration] = state.duration }; if let image = currentArtwork { info[MPMediaItemPropertyArtwork] = MPMediaItemArtwork(boundsSize: image.size) { _ in image } }; center.nowPlayingInfo = info; center.playbackState = state.status == "paused" ? .paused : .playing }
    public func clearNowPlaying() { MPNowPlayingInfoCenter.default().nowPlayingInfo = nil; MPNowPlayingInfoCenter.default().playbackState = .stopped }
    private func registerRemoteCommands() { let center = MPRemoteCommandCenter.shared(); center.playCommand.addTarget { [weak self] _ in Task { @MainActor in try? self?.resume() }; return .success }; center.pauseCommand.addTarget { [weak self] _ in Task { @MainActor in self?.pause() }; return .success }; center.stopCommand.addTarget { [weak self] _ in Task { @MainActor in self?.stop() }; return .success }; center.togglePlayPauseCommand.addTarget { [weak self] _ in Task { @MainActor in guard let self else { return }; if self.paused { try? self.resume() } else { self.pause() } }; return .success } }
}

private enum AudioError: LocalizedError {
    case invalidReference, nothingPlaying, unknownCommand
    var code: String { switch self { case .invalidReference: return "invalid_reference"; case .nothingPlaying: return "nothing_playing"; case .unknownCommand: return "unknown_command" } }
    var errorDescription: String? { switch self { case .invalidReference: return "audio playback requires a valid URL and session"; case .nothingPlaying: return "nothing is playing to resume"; case .unknownCommand: return "unknown JSON-RPC method" } }
}
