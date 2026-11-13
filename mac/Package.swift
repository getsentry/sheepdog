// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "ReviewQueue",
    platforms: [.macOS(.v15)],
    targets: [
        .executableTarget(name: "ReviewQueue"),
        .testTarget(name: "ReviewQueueTests", dependencies: ["ReviewQueue"]),
    ]
)
