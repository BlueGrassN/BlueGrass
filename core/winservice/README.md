# Windows service connection and exit observation

The service connects using the current authorized node configuration. A
prepared runtime can open its business gate and receive the exact TUN interface
permission without first calling `ProbeExit`. `Profile.ExpectedExitIPv4` is
legacy, optional metadata and is not an authorization or equality check.
`VerifiedIPv4()` is retained as a legacy observation accessor; an empty value
does not prevent connection, diagnostics or physical-network monitoring.

`ProbeExit` observes the selected channel after connection. Diagnostic success
is `proxy_exit` with status `passed`, code `observed_exit` and the observed
public IPv4 address, retaining the D33 observation scope. An IPv6 response is
rejected by the producer, which may try its bounded fallback; it is not published
as a successful observation. A different public IPv4 address, rejected response,
unavailable detector or probe timeout does not close the runtime, revoke WFP
permits, clear the cache or release protection. The service forwards the captured diagnostic result;
it does not interpret an old producer's mismatch status as a teardown command.
Nativehost and the runtime own their corresponding connection/report changes.

These observations do not relax the existing lifecycle boundaries:

- Prepare records protection intent and blocks before resolving the entry;
  service DNS retains its explicit physical-interface binding and exact rules.
- Owner SID, resource/logical-session binding, successful runtime preparation,
  current session, interface LUID and WFP permission still govern activation.
- A runtime gate error, interface-permission error or cancelled activation
  revokes the current session and blocks before closing its exact owned TUN.
  Unconfirmed cleanup remains recorded; it cannot become a successful release.
- Physical-network generation changes and snapshot errors are checked even
  before any exit observation. Diagnostics still require the current allowed
  session, valid binding and generation; cancellation or replacement discards
  late results without affecting a newer session.
- UDP relay callbacks remain scoped to the current allowed session; diagnostic
  results cannot grant new endpoints or authorize an old callback.

The service composition tests replace WFP, runtime constructors, DNS resolution,
network snapshots, adapter repair and cache deletion with fixtures. They cover
no-probe connection, ignored legacy expected metadata, changed/failed exit
observations, network monitoring with no observed address, activation failures,
and stale/cancelled diagnostic isolation. They do not establish real elevated
WFP, reboot, installed-service or production-network acceptance.

Run from `core`:

```powershell
& 'D:\Program Files\Go\bin\go.exe' test -mod=readonly -tags with_gvisor -count=1 ./winservice
```
