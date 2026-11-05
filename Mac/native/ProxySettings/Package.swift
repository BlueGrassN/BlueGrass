// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "BlueGrassProxySettings",
    platforms: [.macOS(.v13)],
    products: [.executable(name: "BlueGrass.ProxySettings", targets: ["ProxySettings"])],
    targets: [
        .target(name: "ProxySettingsCore", linkerSettings: [
            .linkedFramework("SystemConfiguration"), .linkedFramework("Security")
        ]),
        .executableTarget(name: "ProxySettings", dependencies: ["ProxySettingsCore"]),
        .testTarget(name: "ProxySettingsCoreTests", dependencies: ["ProxySettingsCore"])
    ]
)
