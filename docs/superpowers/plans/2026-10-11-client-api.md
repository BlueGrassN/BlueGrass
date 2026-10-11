# Independent Client API Implementation Plan

> 历史 PHP 实施计划，已由 [Go 实施计划](2026-10-11-client-api-go.md)替代，不再执行下文任务。当前接口与客户端接入见[开发文档](../../development/client-api-integration.md)。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Develop and verify an independent long-lived client authentication and node API and place it in Hytron's existing site directory.

**Architecture:** Laravel HTTP boundary, read-only customer/node query repositories, independent persistent SQLite session store with atomic refresh rotation and retry receipts. User manages the BaoTa site and HTTPS for test-api.dos.im.

**Tech Stack:** Laravel 12, PHP 8.2, source MySQL, private SQLite sessions, PHPUnit 11.

**Spec:** ../specs/2026-10-11-client-api-design.md

## Global Constraints

- All code in APP/client-api; sunipip-dash is read-only.
- Do not configure sites, Nginx, certificates, SSL, DNS or reload services; user manages these.
- Only Hytron and its suntest database; never contact production servers or upstream APIs.
- No commits, pushes, branch changes or overwriting existing APP work.
- Secrets stay in ignored runtime environment, never documentation or logs.
- 1800-second access TTL, 180-day sliding refresh idle TTL, 120-second idempotent refresh retry receipt.
- Only active forwarded nodes; never raw-IP fallback.

## Review Focus

- Lost refresh response, duplicate request and concurrent retry must not roll credentials backward.
- Disabled/deleted customer, password change and revoked session invalidate credentials.
- Node query must enforce customer ownership and actual relay readiness without business side effects.
- Runtime TLS/proxy headers must not allow plaintext credentials or forged HTTPS headers.
- Tests and migrations must never write source customer/subscription tables.

### Task 1: Runtime and authentication

**Files:** client-api/bootstrap, config, app/Auth, app/Http, database/migrations, tests/TestCase.php, tests/Feature/AuthTest.php.

**Interfaces:** CustomerRepository::byLogin(string), ::byId(int); SessionService::login(array), ::refresh(string,string), ::authenticate(string), ::logout(object); request attributes client_session/client_customer.

- [x] Scaffold isolated Laravel and install locked dependencies on Hytron with a private modern Composer.
- [x] Write and run failing HTTP auth tests: login only verifies source credentials; independent tokens; access expiry; refresh rotation/retry/replay; sliding idle; password/status/ownership revocation; logout; duplicate-device isolation.
- [x] Implement repositories, migration, session service and JSON middleware/routes.
- [x] Run `php vendor/bin/phpunit --filter AuthTest`; require all assertions pass.

### Task 2: Nodes

**Files:** client-api/app/Nodes, app/Http/Controllers/NodeController.php, tests/Feature/NodeTest.php.

**Interfaces:** NodeRepository::paginate(int,array), ::find(int,int); consumes authenticated Customer id, emits normalized node records.

- [x] Write failing cases: other user, expired/cancelled/pending, missing/inactive relay, malformed endpoint, soft-deleted/released IP, precedence, pagination, timezone, no raw-IP fallback.
- [x] Implement read-only queries and normalization with explicit fields.
- [x] Run the complete `php vendor/bin/phpunit` suite.

### Task 3: Hytron deployment and contract

**Files:** client-api/README.md, docs/API.md, scripts/verify-source.php, docs/verification.md.

**Interfaces:** source config is read-only MySQL; default session DB is a private local file; public HTTPS paths match contract.

- [x] Supply environment template and initialization instructions; keep development source credentials outside the public site. Site runtime configuration is left to the user under the narrowed code-only scope.
- [x] Verify source schema, connectivity and read-only session using metadata/counts only; never emit customer data.
- [x] Place code in the existing BaoTa site directory without touching website/SSL configuration; preserve panel-created files.
- [x] Verify application health, validation, anonymous denial, successful synthetic test flows and DB/source separation; no actual customer password required.
- [x] Save API usage, renewal retry rules, local/source deployment verification, installed versions and runtime paths.
- [x] Fresh code review, fix meaningful findings, final complete suite, dependency audit, compare local/remote source hashes. No Git commit/push.
