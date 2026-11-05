# K06 implementation record

Scope: Windows/packages/data_source only. No commits, branches, UI changes or live credentials.
The user's explicit interface supersedes kickoff's proposed data_api_v2 naming.
SessionStore uses one JSON-compatible encrypted record; no extra snapshot persistence abstraction.
Use ../domain once the domain worker provides the agreed types.
Steps: session behavior tests, coordinator; HTTP/DTO behavior tests, adapter; analyze/test; ablation and review.

Completed: public UI interface and export, real HTTP transport, v1 DTO mapping,
single-flight/persistent refresh coordinator, generation isolation and tests.
RED: initial compilable skeleton behavior tests failed before implementations.
Additional regressions reproduced terminal-auth late-read acceptance and premature
restore completion, then passed after fixes. Four mutation experiments confirmed
necessary protections. Removed duplicated IPv4 validation in favor of domain rule.
Final evidence: docs/verification.md, 68 tests and clean analyze.
Final review: self-review (no subagent tool); no commits or changes outside scope.
