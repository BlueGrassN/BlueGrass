# bluegrass-diagnostics

Build from `core`:

```powershell
& 'D:\Program Files\Go\bin\go.exe' build -o ./cmd/bluegrass-diagnostics/out/bluegrass-diagnostics.exe ./cmd/bluegrass-diagnostics
```

Supply exactly one UTF-8 JSON object on stdin and **close stdin**. No arguments,
profiles, environment credentials or config files are read. Input is limited to
8192 bytes and five seconds. Probe execution has a separate 30-second maximum;
Ctrl+C cancels it. One bounded JSON object (at most 16 KiB) is written to stdout;
there is no diagnostic stderr logging, persistent output or automatic upload.

No-network smoke check:

```powershell
'{}' | & ./cmd/bluegrass-diagnostics/out/bluegrass-diagnostics.exe
```

Unauthenticated API health only (makes one configured HTTPS request):

```powershell
'{"api_base_url":"https://client-api.sunip.cc/api/client/v1"}' | & ./cmd/bluegrass-diagnostics/out/bluegrass-diagnostics.exe
```

Exit code 0 means a report was emitted, **not that checks passed**. Read each
result's `status`, `code`, `generation` and `cancelled` binding. Exit 2 means
invalid/timed-out/cancelled input or unsupported arguments; exit 1 means output
or report failure. Invalid input produces only a fixed error code, never an
echo of submitted values. Unknown fields and duplicate JSON keys are rejected.

For the selected-channel, DNS, nonce protocol and stale-generation integration
rules, see [the package contract](../../diagnostics/README.md). This command
only observes network operations; it never modifies WFP/TUN/system proxy,
starts a business session or changes an existing connection's state.
Custom DNS accepts 1–8 ordered candidates sharing the existing per-probe
budget. A host without an authorized physical path may send `{}` for an
all-skipped report; the helper never adds itself to a TUN/WFP whitelist.
