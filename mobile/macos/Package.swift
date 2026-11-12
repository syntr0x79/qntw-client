// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "QuantwayMac",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "Quantway", targets: ["QuantwayApp"])],
    targets: [
        .binaryTarget(name: "QuantwayEngine", path: "build/QuantwayEngine.xcframework"),
        .target(name: "QuantwayCore", dependencies: ["QuantwayEngine"], linkerSettings: [.linkedLibrary("resolv")]),
        .executableTarget(name: "QuantwayApp", dependencies: ["QuantwayCore"], resources: [.copy("Resources/UI")]),
        .testTarget(name: "QuantwayCoreTests", dependencies: ["QuantwayCore"])
    ]
)
