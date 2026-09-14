// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "lilt-player",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "lilt-player", targets: ["LiltPlayer"])],
    targets: [
        .target(name: "LiltPlayerLogic", path: "Sources/LiltPlayerLogic"),
        .executableTarget(name: "LiltPlayer", dependencies: ["LiltPlayerLogic"], path: "Sources/LiltPlayer"),
        .testTarget(name: "LiltPlayerTests", dependencies: ["LiltPlayerLogic"], path: "Tests/LiltPlayerTests")
    ]
)
