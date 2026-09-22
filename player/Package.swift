// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "lilt-player",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "lilt-player", targets: ["LiltPlayer"]),
        .executable(name: "lilt-audio", targets: ["LiltAudio"]),
    ],
    targets: [
        .target(name: "LiltPlayerLogic", path: "Sources/LiltPlayerLogic"),
        .target(name: "LiltHelperKit", dependencies: ["LiltPlayerLogic"], path: "Sources/LiltHelperKit"),
        .executableTarget(name: "LiltPlayer", dependencies: ["LiltPlayerLogic", "LiltHelperKit"], path: "Sources/LiltPlayer"),
        .executableTarget(name: "LiltAudio", dependencies: ["LiltPlayerLogic", "LiltHelperKit"], path: "Sources/LiltAudio"),
        .testTarget(name: "LiltPlayerTests", dependencies: ["LiltPlayerLogic"], path: "Tests/LiltPlayerTests")
    ]
)
