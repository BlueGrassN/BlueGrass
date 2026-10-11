# Go Client API Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans; follow TDD and record results. No commits/push or site configuration.

**Goal:** Replace PHP with a production-schema-compatible Go API with bounded DB/password concurrency.
**Spec:** ../specs/2026-10-11-client-api-go-design.md
**Architecture:** net/http admission control, independently pooled read-only MySQL source and InnoDB session store, indexed login/phone directory, cursor node reads.

## Global constraints

Only APP code and Hytron development/code placement. sunipip-dash read-only. No production connections, no global DB changes/index DDL, no site/SSL edits or service restart. Tokens/passwords/config secrets never in tracked code or reports.

## Tasks

- [x] Define shared model interfaces and strict configuration; archive PHP source as reference; install private Go toolchain.
- [x] Write source repository tests for production eligibility, released-then-renewed resources, password field fallback, indexed lookup and bounded cursor reads; implement source and phone directory.
- [x] Write tests for InnoDB login/refresh concurrency, replay/retry, revocation and ownership; implement session store and crypto/auth layer.
- [x] Add HTTP validation, per-account/IP throttles, request/password admission and independent deadlines; verify overload preserves sessions.
- [x] Run real MySQL integration with random isolated test tables, pool cap/cancellation/recovery and burst-login measurements, race detector and vet.
- [x] Independent review and necessary fixes; replace server PHP code with verified Go source/binary in correct directory, preserve panel files/settings; no automatic public-service startup.
- [x] Document production schema/indices, two-role DB access, instance connection budget, API cursor contract, real validation results and runtime prerequisites.
