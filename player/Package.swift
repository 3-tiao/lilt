// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "lilt-player",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "lilt-player", targets: ["LiltPlayer"])],
    targets: [.executableTarget(name: "LiltPlayer", path: "Sources/LiltPlayer")]
)
