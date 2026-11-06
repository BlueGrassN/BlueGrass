# Connect first, observe exit afterwards

The explicit product correction on 2026-10-12 supersedes the old expected-exit
admission requirement. A complete, eligible profile is sufficient to prepare a
connection. `expectedExitIpv4` is optional legacy input and is ignored; no entry
address or probe result is promoted into an expected-exit requirement.

The controller serializes Prepare, rechecks current attempt and resource expiry,
opens the business gate, and commits `connected` before calling ProbeExit on that
attempt's cancellable context. A probe error, unreachable result, or different
observed address does not close the connection. Only a successful trusted public
IPv4 observation is published as the optional existing `verifiedExitIpv4` field.
The field name is retained for IPC compatibility; it is not an admission proof.

Cancellation, expiry, account/owner changes, resource changes and explicit stop
still invalidate the attempt and close forwarding. Observations are cleared on
invalidation; late engine results cannot populate the new attempt. Nativehost
publishes the controller's accepted observation, never an unbound engine cache.
Connection preparation or gate-application errors remain connection failures.

Selected-resource diagnostics use `passed/observed_exit` and remain scoped by the
exact current context and job generation. Missing observation, failed diagnostics,
and changed observations never authorize a pause, disconnect or protection release.
The host publishes a validated completed `proxy_exit: passed/observed_exit` through
the controller's full-context, open-gate and current-lease checks. Acceptance updates
`verifiedExitIpv4` and increments the event sequence once for that completed job.
A pending initial probe cannot overwrite a newer diagnostic observation.
