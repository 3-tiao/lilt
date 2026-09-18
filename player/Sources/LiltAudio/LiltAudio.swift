import AppKit
import Darwin
import LiltHelperKit

@main
@MainActor
final class LiltAudio: NSObject, NSApplicationDelegate {
    private static var retainedDelegate: LiltAudio?
    private let service = AudioService()
    private var server: RPCSocketServer?
    private var signals: [DispatchSourceSignal] = []

    static func main() {
        let arguments = CommandLine.arguments
        guard let index = arguments.firstIndex(of: "--rpc-socket"), arguments.indices.contains(index + 1) else {
            fputs("usage: lilt-audio --rpc-socket <path>\n", stderr); exit(EXIT_FAILURE)
        }
        let app = NSApplication.shared; app.setActivationPolicy(.accessory)
        let delegate = LiltAudio(); retainedDelegate = delegate; app.delegate = delegate
        let server = RPCSocketServer(path: arguments[index + 1], service: delegate.service); delegate.server = server; delegate.service.connect(server)
        do { try server.start() } catch { fputs("lilt-audio could not start RPC socket: \(error.localizedDescription)\n", stderr); exit(EXIT_FAILURE) }
        delegate.installSignals(); app.run()
    }
    func applicationWillTerminate(_ notification: Notification) { server?.stop(); service.stop() }
    private func installSignals() { for number in [SIGINT, SIGTERM] { Darwin.signal(number, SIG_IGN); let source = DispatchSource.makeSignalSource(signal: number, queue: .main); source.setEventHandler { NSApplication.shared.terminate(nil) }; source.resume(); signals.append(source) } }
}
