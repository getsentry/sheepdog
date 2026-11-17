// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Sheepdog",
    platforms: [.macOS(.v15)],
    targets: [
        .executableTarget(name: "Sheepdog"),
        .testTarget(name: "SheepdogTests", dependencies: ["Sheepdog"]),
    ]
)
