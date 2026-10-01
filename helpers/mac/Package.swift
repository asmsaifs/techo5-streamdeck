// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "deckcap-mac",
    platforms: [.macOS(.v13)],
    targets: [
        .executableTarget(name: "deckcap-mac", path: "Sources/deckcap-mac"),
    ]
)
