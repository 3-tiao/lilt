import AppKit
import AVFoundation
import Darwin
import Foundation
import MusicKit

final class FreshMusicTokenProvider: MusicUserTokenProvider, MusicDeveloperTokenProvider, @unchecked Sendable {
    private let provider = DefaultMusicTokenProvider()

    override init() { super.init() }

    func developerToken(options: MusicTokenRequestOptions) async throws -> String {
        try await provider.developerToken(options: options.union(.ignoreCache))
    }
}

struct PlaybackRequest: Codable { let kind: String; let id: String?; let storefront: String?; let url: String?; let startAt: Int? }
struct JSONValue: Codable {
    private let storage: Storage
    private enum Storage { case string(String), int(Int), bool(Bool), object([String: JSONValue]), null }
    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { storage = .null }
        else if let value = try? c.decode(Bool.self) { storage = .bool(value) }
        else if let value = try? c.decode(Int.self) { storage = .int(value) }
        else if let value = try? c.decode(String.self) { storage = .string(value) }
        else { storage = .object(try c.decode([String: JSONValue].self)) }
    }
    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch storage {
        case .string(let value): try c.encode(value)
        case .int(let value): try c.encode(value)
        case .bool(let value): try c.encode(value)
        case .object(let value): try c.encode(value)
        case .null: try c.encodeNil()
        }
    }
    var string: String? { if case .string(let value) = storage { return value }; return nil }
    var int: Int? { if case .int(let value) = storage { return value }; return nil }
    var bool: Bool? { if case .bool(let value) = storage { return value }; return nil }
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
struct State: Codable { let track: Track?; let position: Double; let duration: Double; let status: String; let audioVariant: String?; let format: String; let availableFormats: [String]; let shuffle: Bool; let repeatMode: String; let isLive: Bool; let mode: String; let authorization: String; let queue: [Track]; let queueIndex: Int }
struct Track: Codable { let kind: String; let id: String?; let url: String?; let title: String; let artist: String?; let previewURL: String? }
struct ITunesSearchResponse: Decodable { let results: [ITunesSong] }
struct ITunesSong: Decodable { let trackId: Int; let trackName: String; let artistName: String; let trackViewUrl: String?; let previewUrl: String? }
enum Result: Encodable {
    case state(State), authorization(Authorization), diagnostics(TokenDiagnostics), hello(Hello), tracks([Track]), empty
    func encode(to encoder: Encoder) throws {
        switch self {
        case .state(let value): try value.encode(to: encoder)
        case .authorization(let value): try value.encode(to: encoder)
        case .diagnostics(let value): try value.encode(to: encoder)
        case .hello(let value): try value.encode(to: encoder)
        case .tracks(let value): try value.encode(to: encoder)
        case .empty: var c = encoder.singleValueContainer(); try c.encode([String: String]())
        }
    }
}
struct RPCResponse: Encodable { let jsonrpc = "2.0"; let id: Int; let result: Result?; let error: RPCError? }

final class RPCSocketServer {
    private let path: String
    private let lock = NSLock()
    private var listener: Int32 = -1
    private var connection: Int32 = -1
    private var stopped = false
    private var hostConnected = false
    private var watchdog: DispatchWorkItem?

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
        let encoder = JSONEncoder()
        do {
            for try await line in file.bytes.lines {
                guard let request = try? decoder.decode(RPCRequest.self, from: Data(line.utf8)), request.jsonrpc == "2.0" else { continue }
                let (response, shouldShutdown) = await LiltPlayer.handle(request)
                var data = try encoder.encode(response)
                data.append(0x0A)
                try file.write(contentsOf: data)
                if shouldShutdown { break }
            }
        } catch {
            fputs("lilt-player RPC connection ended: \(error.localizedDescription)\n", stderr)
        }
        stop()
        LiltPlayer.stopPlayback()
        await MainActor.run { NSApplication.shared.terminate(nil) }
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
        if connectionFD >= 0 { Darwin.shutdown(connectionFD, SHUT_RDWR); Darwin.close(connectionFD) }
        if listenerFD >= 0 { Darwin.shutdown(listenerFD, SHUT_RDWR); Darwin.close(listenerFD) }
        Darwin.unlink(path)
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

@main final class LiltPlayer: NSObject, NSApplicationDelegate {
    private static var retainedDelegate: LiltPlayer?
    private static var previewPlayer: AVPlayer?
    private static var streamPlayer: AVPlayer?
    private static var currentTrack: Track?
    private static var mode = "none"
    private static var variantCache: [String: [String]] = [:]
    private static var variantInFlight: Set<String> = []
    private static var recentlyPlayedCloudUnavailable = false
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

    static func dispatch(_ request: RPCRequest) async throws -> Result {
        switch request.method {
        case "ping": return .hello(Hello(pid: getpid()))
        case "authorize":
            if request.params?["request"]?.bool == true && MusicAuthorization.currentStatus == .notDetermined {
                await MainActor.run { NSApplication.shared.activate(ignoringOtherApps: true) }
                _ = await MusicAuthorization.request()
            }
            return .authorization(await authorization())
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
            default: ApplicationMusicPlayer.shared.state.repeatMode = .none
            }
            return .state(state())
        case "stop":
            stop()
            return .state(state())
        case "enqueue":
            try await enqueue(request.params)
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
    static func authorization() async -> Authorization {
        let status = authorizationStatus()
        guard status == "authorized" else {
            return Authorization(status: status, accountStatus: nil, accountError: nil, countryCode: nil, canPlayCatalogContent: false, hasCloudLibraryEnabled: false)
        }
        do {
            let subscription = try await MusicSubscription.current
            let accountStatus: String
            if !subscription.canPlayCatalogContent {
                accountStatus = "subscription_required"
            } else if !subscription.hasCloudLibraryEnabled {
                accountStatus = "cloud_library_disabled"
            } else {
                accountStatus = "ready"
            }
            let countryCode = try? await MusicDataRequest.currentCountryCode
            return Authorization(status: status, accountStatus: accountStatus, accountError: nil, countryCode: countryCode, canPlayCatalogContent: subscription.canPlayCatalogContent, hasCloudLibraryEnabled: subscription.hasCloudLibraryEnabled)
        } catch {
            return Authorization(status: status, accountStatus: "account_unavailable", accountError: errorDetails(error), countryCode: nil, canPlayCatalogContent: false, hasCloudLibraryEnabled: false)
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
        if let entries = playlist.entries { return entries.map(entryTrack) }
        return []
    }
    static func libraryPlaylistEntries(_ id: String) async throws -> [Track] {
        var request = MusicLibraryRequest<Playlist>()
        request.filter(matching: \.id, equalTo: MusicItemID(id))
        guard let playlist = try await request.response().items.first else { return [] }
        if let entries = playlist.entries, !entries.isEmpty { return entries.map(entryTrack) }
        if let full = try? await playlist.with([.entries]), let entries = full.entries, !entries.isEmpty {
            return entries.map(entryTrack)
        }
        if let full = try? await playlist.with([.tracks]), let tracks = full.tracks, !tracks.isEmpty {
            return tracks.map { Track(kind: "song", id: $0.id.rawValue, url: nil, title: $0.title, artist: $0.artistName, previewURL: nil) }
        }
        return []
    }
    static func entryTrack(_ entry: MusicKit.Playlist.Entry) -> Track {
        Track(kind: "song", id: entry.item?.id.rawValue, url: entry.url?.absoluteString, title: entry.title, artist: entry.artistName, previewURL: entry.previewAssets?.first?.url?.absoluteString)
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
        guard let url = params?["url"]?.string, !url.isEmpty, let components = URLComponents(string: url), let id = canonicalID(PlaybackRequest(kind: "", id: nil, storefront: nil, url: url, startAt: nil)) else { throw PlayerError.invalidReference }
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
        let request = PlaybackRequest(kind: kind, id: params["id"]?.string, storefront: params["storefront"]?.string, url: params["url"]?.string, startAt: params["startAt"]?.int)
        guard ["song", "playlist", "station"].contains(request.kind), let id = canonicalID(request) else { throw PlayerError.invalidReference }
        streamPlayer?.pause(); streamPlayer = nil
        if authorizationStatus() != "authorized" {
            guard request.kind == "song" else { throw PlayerError.authorizationRequired }
            try await playPreview(id: id)
            return
        }
        do {
            if request.kind == "playlist" {
                var library = MusicLibraryRequest<Playlist>()
                library.filter(matching: \.id, equalTo: MusicItemID(id))
                guard let playlist = try await library.response().items.first else { throw PlayerError.invalidReference }
                currentTrack = Track(kind: "playlist", id: playlist.id.rawValue, url: playlist.url?.absoluteString, title: playlist.name, artist: playlist.curatorName, previewURL: nil)
                previewPlayer?.pause(); mode = "full"
                ApplicationMusicPlayer.shared.queue = .init(for: [playlist])
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
    static func enqueue(_ params: [String: JSONValue]?) async throws {
        guard let params, let kind = params["kind"]?.string else { throw PlayerError.invalidReference }
        let request = PlaybackRequest(kind: kind, id: params["id"]?.string, storefront: params["storefront"]?.string, url: params["url"]?.string, startAt: nil)
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
        mode = "none"
    }
    static func canonicalID(_ request: PlaybackRequest) -> String? {
        if let id = request.id, !id.isEmpty { return id.contains(":") ? String(id.split(separator: ":", maxSplits: 1)[1]) : id }
        guard let url = request.url, let components = URLComponents(string: url), components.host?.hasSuffix("music.apple.com") == true else { return nil }
        return components.queryItems?.first(where: { $0.name == "i" })?.value ?? components.path.split(separator: "/").last.map(String.init)
    }
    static func pause() {
        if mode == "full" { ApplicationMusicPlayer.shared.pause() }
        else if mode == "stream" { streamPlayer?.pause() }
        else { previewPlayer?.pause() }
    }
    static func resume() async throws {
        if mode == "full" { try await ApplicationMusicPlayer.shared.play() }
        else if mode == "stream" { guard let streamPlayer else { throw PlayerError.previewUnavailable }; streamPlayer.play() }
        else if let previewPlayer { previewPlayer.play() }
        else { throw PlayerError.previewUnavailable }
    }
    static func stopPlayback() {
        previewPlayer?.pause()
        streamPlayer?.pause()
        ApplicationMusicPlayer.shared.pause()
        mode = "none"
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
            return State(track: track, position: player.playbackTime, duration: duration(of: current) ?? 0, status: String(describing: player.state.playbackStatus), audioVariant: player.state.audioVariant.map { String(describing: $0) }, format: formatLabel(player.state.audioVariant), availableFormats: availableFormats(for: track?.id), shuffle: player.state.shuffleMode == .songs, repeatMode: repeatLabel(player.state.repeatMode), isLive: false, mode: mode, authorization: authorizationStatus(), queue: queue, queueIndex: index)
        }
        if mode == "stream" {
            let seconds = streamPlayer?.currentTime().seconds ?? 0
            let status: String
            switch streamPlayer?.timeControlStatus {
            case .playing: status = "playing"
            case .waitingToPlayAtSpecifiedRate: status = "buffering"
            default: status = "paused"
            }
            return State(track: currentTrack, position: seconds.isFinite ? seconds : 0, duration: 0, status: status, audioVariant: nil, format: "live stream", availableFormats: [], shuffle: false, repeatMode: "off", isLive: true, mode: mode, authorization: authorizationStatus(), queue: [], queueIndex: 0)
        }
        let seconds = previewPlayer?.currentTime().seconds ?? 0
        let status: String
        if mode == "none" { status = "stopped" }
        else { switch previewPlayer?.timeControlStatus { case .playing: status = "playing"; case .waitingToPlayAtSpecifiedRate: status = "buffering"; default: status = "paused" } }
        let previewFormat = mode == "preview" ? "AAC preview" : "—"
        return State(track: currentTrack, position: seconds.isFinite ? seconds : 0, duration: 0, status: status, audioVariant: nil, format: previewFormat, availableFormats: [], shuffle: false, repeatMode: "off", isLive: false, mode: mode, authorization: authorizationStatus(), queue: [], queueIndex: 0)
    }
    static func radioPlay(_ params: [String: JSONValue]?) throws {
        guard let urlString = params?["url"]?.string, let url = URL(string: urlString) else { throw PlayerError.invalidReference }
        ApplicationMusicPlayer.shared.stop()
        previewPlayer?.pause(); previewPlayer = nil
        streamPlayer?.pause()
        currentTrack = Track(kind: "stream", id: nil, url: urlString, title: params?["name"]?.string ?? urlString, artist: nil, previewURL: nil)
        mode = "stream"
        let player = AVPlayer(url: url)
        streamPlayer = player
        player.play()
    }
    static func radioStop() {
        streamPlayer?.pause()
        streamPlayer = nil
        mode = "none"
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
        guard let variant else { return "Auto" }
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
        mode = "preview"
        previewPlayer = AVPlayer(url: url)
        previewPlayer?.play()
    }
    static func iTunesTrack(_ song: ITunesSong) -> Track {
        Track(kind: "song", id: String(song.trackId), url: song.trackViewUrl, title: song.trackName, artist: song.artistName, previewURL: song.previewUrl)
    }
}

enum PlayerError: LocalizedError {
    case invalidReference, invalidSearch, previewUnavailable, previewSearchUnavailable, previewUnsupported, authorizationRequired, queueUnavailable, unknownMethod
    var code: String {
        switch self {
        case .previewUnavailable: return "preview_unavailable"
        case .previewSearchUnavailable: return "preview_search_unavailable"
        case .previewUnsupported: return "preview_unsupported"
        case .authorizationRequired: return "authorization_required"
        case .queueUnavailable: return "queue_unavailable"
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
        case .unknownMethod: return "unknown JSON-RPC method"
        }
    }
}
