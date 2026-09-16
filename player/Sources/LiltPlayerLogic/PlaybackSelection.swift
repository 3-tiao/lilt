public struct StartTrack: Equatable, Sendable {
    public let id: String?
    public let title: String

    public init(id: String?, title: String) {
        self.id = id
        self.title = title
    }
}

public func selectedStartIndex(tracks: [StartTrack], id: String?, index: Int?, title: String?) -> Int {
    guard !tracks.isEmpty else { return 0 }
    if let id, !id.isEmpty, let match = tracks.firstIndex(where: { $0.id == id }) { return match }
    if let index, tracks.indices.contains(index) { return index }
    if let title, !title.isEmpty, let match = tracks.firstIndex(where: { $0.title == title }) { return match }
    return 0
}

public struct PlaylistEntryDescriptor: Equatable, Sendable {
    public let kind: String
    public let id: String?
    public let title: String
    public let available: Bool
    public let originalIndex: Int

    public init(kind: String, id: String?, title: String, available: Bool = true, originalIndex: Int) {
        self.kind = kind
        self.id = id
        self.title = title
        self.available = available
        self.originalIndex = originalIndex
    }
}

// supportedPlaylistEntries models the browse/play contract independently of
// MusicKit: only available songs enter either surface, while their source index
// remains available for diagnostics and stable mapping.
public func supportedPlaylistEntries(_ entries: [PlaylistEntryDescriptor], reverse: Bool = false) -> [PlaylistEntryDescriptor] {
    var supported = entries.filter { $0.available && $0.kind == "song" }
    if reverse { supported.reverse() }
    return supported
}

public func musicAccountStatus(canPlayCatalogContent: Bool, hasCloudLibraryEnabled: Bool) -> String {
    if !canPlayCatalogContent { return "subscription_required" }
    if !hasCloudLibraryEnabled { return "cloud_library_disabled" }
    return "ready"
}

// probeErrorCode maps an AVFoundation/URL loading failure to the stable probe
// error vocabulary the UI displays and localizes.
public func probeErrorCode(domain: String, code: Int) -> String {
    switch domain {
    case "NSURLErrorDomain":
        switch code {
        case -1200, -1201, -1202, -1203, -1204, -1205, -1206: return "tls"
        case -1001: return "timeout"
        case -1002, -1003, -1004, -1005, -1008, -1009: return "network"
        default: return "network"
        }
    case "AVFoundationErrorDomain":
        switch code {
        case -11828: return "unsupported"
        default: return "unknown"
        }
    default:
        return "unknown"
    }
}

// probeHTTPResponseOutcome keeps response classification independent of the
// URLSession delegate, so the probe's wire-level contract is testable without
// making a network request.
public enum ProbeHTTPResponseOutcome: Equatable, Sendable {
    case healthy
    case httpError
    case closedWithoutData
}

public func probeHTTPResponseOutcome(statusCode: Int, receivedData: Bool) -> ProbeHTTPResponseOutcome {
    if statusCode >= 400 { return .httpError }
    if receivedData { return .healthy }
    return .closedWithoutData
}
