# BlueGrass macOS launcher

This thin Flutter app imports the production desktop entry point from
`Windows/app`. Pages, navigation, components, theme and business rules remain
shared; the `macos/` runner implements native Mac capabilities.

Before analysis/tests/builds, synchronize canonical assets from the repository
root with `python Mac/tools/sync_assets.py`. Follow [the Mac development entry](../README.md)
and [test-package instructions](../docs/TESTING.md) for complete packaging. A bare
Flutter build does not include the Go and Swift proxy helpers.

The first development build supports system proxy, requires macOS 13+, uses
ad-hoc signing, and explicitly disables TUN and the Windows automatic updater.
