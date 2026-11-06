# BlueGrass Android Go core

This module embeds **Xray-core only**, under [ADR 0049](../../docs/adr/0049-xray-only-four-platforms.md). Read the [Android developer manual](../../docs/development/android.md), [shared four-platform manual](../../docs/development/four-platform-development.md), and [protocol matrix](../../docs/development/xray-protocol-matrix.md).

The selected upstream release is **v26.10.17, marked Pre-release**, commit `92ccc939aaf4f8965f369cf0b142e005aeaa11d5`. Its actual Go module version is `github.com/xtls/xray-core v1.260327.1-0.20261017152356-92ccc939aaf4`; there is no `v1.261017.0` module tag. The pinned upstream module requires Go 1.27. Do not bundle sing-box, keep it as a fallback, or add a second proxy protocol engine.

## Implementation status

On 2026-10-24 the current Xray bridge was rebuilt for arm64-v8a and x86_64. Both ELF architectures, the exact Xray pin and JNI exports were checked. Actual Android 11 emulator instrumentation loaded the library and exercised validation. See [the emulator report](../docs/emulator-verification-20261024.md) for evidence and the limits of the VPN lifecycle checks. Earlier sing-box artifacts remain historical.

Shared protocol configuration, lifecycle and exit observation reuse cross-platform packages under `core/`. Keep the repository layout and isolate Windows-only packages. Kotlin owns Android VPN authorization, foreground lifecycle, network selection and the original TUN descriptor.

Routing accepts only `global` and `bypass_cn`, with omitted legacy configurations retaining `global`. Split mode shares the Windows [routing classifier and pinned rules](../../core/routingpolicy/README.md). Bundled data attribution and licenses are included in [the rule-data notice](../../core/routingpolicy/assets/NOTICE.md). Domestic domain priority is preserved with session-owned FakeIP mappings; physical direct sockets still use protect/Network binding and the session lease. Unknown/stale synthetic addresses fail closed. The process allocator never reassigns an address and is bounded to 131,070 allocations, with at most 8,192 names and 8,192 domain/address bindings in one session. Multiple A answers remain available for application failover; validated answers share a TTL cache. Details are in [the routing contract](core/ROUTING.md). Exit observation always probes the selected node.

## Build and checks

After the migration is integrated, use the Xray-specific entry points from `Android/native/`:

```powershell
..\scripts\build-native.ps1
..\scripts\check-native.ps1 -Race
```

Expected outputs remain `Android/android/app/libs/bluegrass.aar` and `bluegrass-sources.jar`. Missing or obsolete native artifacts must fail the build rather than substitute a fake engine. The [build entry](../scripts/build-native.ps1) checks the Xray module pin, dependency graph and exact ARM64 library set; record its actual Go, gomobile, NDK, JDK, API and ABI versions from the new build. The older `native/build.ps1` path delegates to this entry. Since 2026-10-26 both entry points build ARM64 only; the dual-ABI build above is historical validation.

Run in `Android/native/core/`:

```powershell
go test -mod=readonly -race ./...
go vet -mod=readonly ./...
go list -m github.com/xtls/xray-core
```

These are verification entry points, not results from this documentation task. Reconcile any remaining old build tags with the actual migration before packaging. Extract module information from the built native library, compare the exact pin and checksum in the shared manual, and inspect the final APK rather than trusting source declarations alone.

## Ownership and routing requirements

- Accept a closed resource schema, validate protocol parameters and effective UTC expiry, and reject arbitrary routing/DNS configuration. TUIC and AnyTLS remain recognizable wire types but must fail configuration/outbound creation explicitly.
- All external proxy and controlled DNS sockets must invoke Kotlin's protection and selected Network binding before use. Cover TCP and UDP/QUIC socket paths; a failed protection/binding hook must prevent connection.
- Kotlin retains the system TUN descriptor. The pinned Xray Android TUN implementation duplicates its input descriptor and closes its duplicate. Document any additional bridge-owned duplicate, serialize lifecycle/global hooks, and close exactly the descriptors each layer owns.
- Pausing forwarding and disconnecting are different operations. Preserve the required blocking VPN during expiry/revocation/failure; an explicit stop cleans up the session. Do not claim success before native confirmation.
- Public business DNS follows the selected proxy; entry DNS uses the controlled underlying network. Explicit local DNS/mDNS and LAN exceptions must not expand into a public direct fallback. Maintain the current public IPv6 blocking policy.
- Actual IPv4 exit observation uses the selected outbound after connection. Failure is reported independently and does not tear down an otherwise valid connection or restore the withdrawn expected-exit gate.
- Configuration stays in memory. Return bounded errors and never log node credentials, profile JSON or account tokens.

The retained `github.com/sagernet/sing-tun` utility is OS packet/TUN plumbing only; it is not the `github.com/sagernet/sing-box` proxy engine. Xray remains the sole remote protocol engine. JNI callbacks, protect/binding, TUN packets and device lifecycle need real Android verification.

## Licensing and historical evidence

The pinned Xray upstream license is MPL-2.0. The new artifact must include notices for its actual dependencies, and the UI license page must match them. This does not automatically relicense BlueGrass or other dependencies. Old sing-box licensing belongs to historical artifacts; removing a notice alone does not prove removal of the engine.

The [previous VPN plan](../../docs/superpowers/plans/2026-10-13-android-vpn.md) and [VPN verification report](../docs/vpn-verification.md) retain their original test/build claims. New Xray results must identify code state, commands, exit codes, artifact hashes and device limitations separately.

