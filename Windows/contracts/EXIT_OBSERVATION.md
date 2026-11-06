# Exit observation contract — 2026-10-12

The latest user instruction supersedes the earlier expected-exit gate: prepare
and connect a valid node, then probe that selected connection for actual IP.
Probe failure leaves the connection established with IP unknown. Expected source
metadata neither authorizes connection nor proves its actual exit.

## Public domain and data-source interface

- `ConnectionProfile.expectedExitIpv4` is now `String?`, matching `NodeRecord`.
  It is optional metadata. `prepareProfile` does not require or validate it.
  Resource identity, forwarding eligibility, exact expiry, entry endpoint,
  supported protocol and authentication/configuration checks remain in force.
- `NativeConfirmed.verifiedExitIpv4` and `ConnectionState.verifiedExitIpv4`
  retain their nullable types. A matching, current connected confirmation with
  forwarding protection is accepted with no IP or with any valid public IPv4.
  The observation is never compared to expected metadata. Missing observations
  clear prior observed values, including on authoritative snapshots. Present
  malformed/special-use observations are rejected. A raw unsuccessful probe
  should be normalized to absent before emitting a connected confirmation.
- Identity/generation, event ordering, termination, expiry and protection checks
  are unchanged. Expected-metadata-only resource revisions do not pause or clear
  the actual observation; the existing `applyResourceUpdate` effect remains.
  Entry/auth/protocol changes still pause; expiry updates still require native ack.
- API `expected_exit_ip` parsing still retains only a valid public IPv4 and
  otherwise yields null; host is never substituted. Both outcomes allow profile
  preparation when the other checks pass, for legacy SOCKS and explicit protocols.
  `authoritative_expected_exit` is informational. True is retained; absent,
  unrecognized or false values report false without blocking capability negotiation.
  Ownership coverage remains conservative/per_page, `snapshotComplete=false`;
  no authentication, session, pagination or full-ownership guarantees were relaxed.

## Shared wire and fixture changes

- `domain/resource.schema.json`: remove `expectedExitIpv4` from ConnectionProfile
  required fields. If retained for old senders it is ignored string/null metadata,
  with no IPv4-format or matching requirement. New GUI profile JSON omits it.
  Other required fields, closed-object validation and explicit non-SOCKS
  `protocolConfig` binding are unchanged.
- `ipc/messages.schema.json`: normalized NativeConfirmation observation is still
  optional/null. Present values use the registered `bluegrass-public-ipv4` format
  checker; use `test_contracts.py`'s checker to enforce classification.
- `fixtures/connection/control-requests.json`: the valid Connect profile now
  omits expected exit. Python additionally accepts legacy populated/null/malformed
  string metadata and rejects wrong JSON types and missing unrelated required fields.
- `fixtures/eligibility/cases.json`: missing and malformed expected metadata are
  accepted. `invalidExpectedIps` has been renamed **`invalidObservedIps`**;
  consumers must use the new key and apply these negative vectors to actual
  observations, not to profile preparation.
- `fixtures/connection/generations.json`: expected metadata at the top level is
  independent of each confirmed event's `verifiedExitIpv4`. Omitted/null event
  observations mean unknown and must never inherit the top-level expected value.
  Different actual IP and omitted/null observed IP now connect. Optional
  `expectedObservedIpv4` asserts the final actual value, including null. Late and
  cancelled attempts still cannot revive. Malformed-observation validation is
  covered by `invalidObservedIps` and normalized confirmation/reducer tests;
  those negatives do not make raw probe failure a connection failure.

## Verification boundary

Scoped Dart and Python checks use synthetic fixtures only; they do not prove
live proxy/exit probing, GUI behavior or native runtime behavior. App/native files
are owned by other implementers and are not edited by this change. In particular,
the GUI must omit expected metadata from the profile payload and active config
comparison, and its live resource integration test must remove the old assertion
that missing expected exit disables selection. Native fixture consumers must adopt
the observation semantics and renamed negative-vector key above.

Commands, executed from the corresponding directory:

```text
Windows/packages/domain: dart analyze; dart test --reporter expanded
Windows/packages/data_source: dart analyze; dart test --reporter expanded
Windows/contracts: py -3.14 test_contracts.py
Windows/packages/domain: py -3.14 tool/ablate.py
```

Local `expected-exit-red.log` records the observed pre-fix failures;
`expected-exit-tests.log` and `expected-exit-analyze.log` record current checks.
The domain ablation report and `.ablation/*.log` record actual behavioral failures
from removing remaining mechanisms in an isolated copy, followed by a restored
green run. The former expected-exit gate is not claimed as a necessary mechanism.

Executed results: domain 162 tests passed, data_source 171 tests passed, Python
contracts 13 tests passed, and both Dart analyzers reported no issues. All six
domain ablations caused behavioral regressions; removing observed-IP validation
caused 24 failures. The restored isolated suite passed all 162 tests. These checks
used no live accounts, database, network configuration changes or deployment.
