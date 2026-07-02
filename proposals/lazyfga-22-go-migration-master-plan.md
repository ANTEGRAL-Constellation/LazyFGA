# Go Backend Migration — Master Plan - Spec Proposal

| Item      | Detail       |
| --------- | ------------ |
| Author    | Seonguk Moon |
| Created   | 2026-07-02   |
| Status    | **Implemented** |
| Reviewers | Claude (review agent), Codex (gpt-5.5) |

---

## 1. Summary

Migrate the lazyFGA backend (`apps/api`, currently Hono on Bun/TypeScript, plus the backend-side usage of `packages/shared` and `packages/compiler`) to **100% Go**, preserving every externally observable behavior: routes, request/response contracts, database schema, OpenFGA interaction semantics, and operational behavior (startup, health, degraded mode). The migration must land **production-ready**: ≥95% statement test coverage on the Go backend enforced by CI, a rewritten CI pipeline, updated Docker/compose/docs, and immediate deployability via `docker compose up`.

This is the master plan. It fixes the architecture, technology stack, cross-cutting decisions, phase/proposal breakdown, and acceptance criteria. Implementation details per area live in the sub-proposals LFGA-23 through LFGA-27, which are governed by this document.

## 2. Background & Motivation

- The current backend is TypeScript on Bun (Hono). The project owner has decided to move the backend to Go for production operation (static binary deployment, mature concurrency/runtime, first-party Go ecosystem around OpenFGA).
- The OpenFGA ecosystem is Go-native: OpenFGA itself, the official `openfga/go-sdk`, and the official `openfga/language` DSL transformer are all Go projects. The current TS backend consumes the same capabilities through JS wrappers (`@openfga/sdk`, `@openfga/syntax-transformer`); migrating removes a translation layer between lazyFGA and its engine.
- The current test story is Bun-based unit tests (17 test files) with no enforced coverage gate. The migration raises the bar: ≥95% enforced coverage on the Go backend.
- Constraint from the current architecture (ARCHITECTURE.md): `packages/compiler` is shared by web (real-time in-browser preview) and api (authoritative validation at publish) so that both sides never drift. A Go backend breaks single-source code sharing, so this plan must define how parity is preserved (see 4.3 D1).

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `apps/api` is 100% Go: every route, module, script, and behavior of the TS backend is reimplemented in Go; the TS backend is deleted at cutover.
- [ ] Behavior parity: identical HTTP contracts (paths, methods, status codes, JSON shapes), identical DB schema, identical OpenFGA call semantics (store bootstrap, transaction-mode writes, pagination, error classification), identical auth semantics (admin token + service tokens, constant-time compare, 401/403/500 separation) — subject only to the short list of **approved deviations** in §4.4, each of which is explicitly tested and documented.
- [ ] Existing deployments keep working: the Go migration runner adopts a database previously migrated by Drizzle (no data loss, no manual steps) and a fresh database initializes identically.
- [ ] ≥95% total statement coverage (`go test ./... -coverprofile`) on the Go backend module, enforced as a hard CI gate.
- [ ] CI (`.github/workflows/ci.yml`) rebuilt: Go lane (build, vet, golangci-lint, tests with real Postgres/OpenFGA services, coverage gate) + Node lane (web + packages) + cross-language compiler parity job.
- [ ] Production-ready and immediately deployable: multi-stage Dockerfile producing a static binary, `docker compose up` brings up postgres + openfga + Go api end-to-end, demo scenario passes against the live stack, docs (README, docs/getting-started.md, docs/api.md) updated.
- [ ] Web UI (`apps/web`) continues to work unchanged against the Go backend (verified E2E).

### 3.2 Non-Goals

- [ ] No functional changes: no new endpoints, no contract changes, no schema changes beyond what parity requires. Feature work is out of scope.
- [ ] No frontend migration: `apps/web` stays TypeScript/React. `packages/shared` and `packages/compiler` remain as TS packages consumed by web (browser-side preview needs to stay in-browser; compiling the Go compiler to WASM is explicitly rejected for this migration — see 4.3 D1).
- [ ] No OpenFGA version or deployment topology changes (still `openfga/openfga:v1.18.0`, single store, single Postgres instance with two databases).
- [ ] No multi-tenancy, no RBAC beyond the existing admin/service roles, no horizontal-scaling work beyond what a stateless Go binary already provides.

## 4. Technical Design

### 4.1 Architecture Overview

The modular-monolith shape is preserved 1:1; only the language and runtime change.

```
lazyfga/
├─ apps/
│  ├─ web/                       # unchanged (Vite + React), still imports TS shared/compiler
│  └─ api/                       # ★ Go (replaces TS at cutover; developed as apps/api-go until LFGA-27)
│     ├─ go.mod                  # module github.com/antegral-constellation/lazyfga/api
│     ├─ cmd/
│     │  ├─ lazyfga-api/main.go  # server entrypoint (thin; calls internal/app.Run)
│     │  ├─ demo/                # demo scenario runner + reset (port of scripts/demo)
│     │  └─ seed-zitadel-rules/  # mapping-rule seeder (port of scripts/seed-zitadel-rules.ts)
│     └─ internal/
│        ├─ app/                 # bootstrap: config load, migrate, FGA bootstrap w/ retry, degraded mode, HTTP server
│        ├─ config/              # env parsing (PORT, DATABASE_URL, OPENFGA_API_URL, LAZYFGA_STORE_ID, ADMIN_TOKEN)
│        ├─ httpx/               # router assembly (chi), middleware (auth), JSON helpers, error mapping
│        ├─ db/                  # pgx pool, drizzle-compatible migrator, embedded migrations
│        ├─ openfga/             # Gateway interface + go-sdk impl + write-error classification
│        ├─ contract/            # ports of packages/shared (backend-consumed subset): model IR, ident, condition, authzen, policy, reason, audit, grant + validation
│        ├─ compiler/            # ports of packages/compiler (backend-consumed subset): ir-to-dsl, condition-to-cel
│        └─ modules/
│           ├─ model/  policy/  pdp/  permission/  auth/  audit/
│           └─ idp/              # signature, extraction, presets, mapping engine, webhook + connection/rule routes
└─ packages/
   ├─ shared/                    # TS: stays for web; __fixtures__ become the cross-language contract corpus
   └─ compiler/                  # TS: stays for web preview; golden fixtures guarantee parity with internal/compiler
```

Request flow, data ownership (lazyFGA Postgres vs OpenFGA store), and the three core flows (model publish / PDP evaluation / IdP sync) are unchanged from ARCHITECTURE.md.

### 4.2 Data Model Changes

**No schema changes.** The seven existing tables (`model_version`, `instance_config`, `service_token`, `policy`, `idp_connection`, `idp_mapping_rule`, `audit_log`) and all constraints/indexes are kept byte-identical.

Migration bookkeeping: the Go backend ships a **drizzle-compatible migrator**. It embeds the existing SQL files (`0000`–`0006`) plus their journal, and reads/writes the same `drizzle.__drizzle_migrations` bookkeeping table (hash + created_at millis, apply entries whose folder-millis exceed the last applied). Consequences:

- A database previously migrated by the TS backend is recognized as fully applied — zero-touch adoption.
- A fresh database is initialized identically to today.
- Future migrations append new SQL files to the same embedded set using the same journal format (the format is frozen and documented in LFGA-23; Drizzle tooling is no longer used to generate them).

### 4.3 Core Logic — Cross-Cutting Decisions

**D1. Compiler parity (the "shared heart" problem).** Today web and api import the same TS compiler, which eliminates drift by construction. After migration there are two implementations: TS (web preview) and Go (authoritative, backend). Drift is prevented by a **cross-language golden fixture corpus**:

- Scope cut that shrinks the drift surface: usage analysis shows the backend **server** consumes only `compileIrToDsl`/`CompileError` from the compiler; `dsl-to-ir`, `coverage`, `describeCondition`, `policyContextParams`, and `edit.ts` are **web-only** and stay TS-only (not ported). Exception: the demo script uses two `edit.ts` helpers (`addCondition`, `setAssignmentCondition`); LFGA-27 reimplements those two operations as demo-local helpers in the Go demo CLI. The dual-implemented logic is exactly: IR→DSL emission, condition→CEL emission, the contract validators, and the two demo edit helpers.
- The corpus lives in `packages/shared/src/__fixtures__/parity/` (next to the existing fixtures): named cases of `(IR JSON, DSL text, condition trees, CEL strings, validation verdicts, grant decisions)` covering every supported construct and every documented edge case, plus invalid inputs with expected error codes.
- The TS test suite and the Go test suite both run against the identical corpus files; CI runs a dedicated **parity job** that fails if either side diverges from the corpus.
- The DSL↔AuthModel-JSON layer itself is delegated on both sides to the official OpenFGA language package (`@openfga/syntax-transformer` in TS, `openfga/language/pkg/go/transformer` in Go), which are generated from the same grammar — lazyFGA only needs parity for its own IR↔DSL and condition→CEL logic.
- Authority rule (unchanged in spirit): the backend (now Go) is authoritative at publish time; the browser preview is advisory. If they ever disagree, publish-time validation wins and the parity corpus gains a regression case.

**D2. HTTP stack.** `chi` v5 router + stdlib `net/http` server, `log/slog` JSON logging. Rationale: route groups per module mirror the current Hono mounting (`/model`, `/tokens`, `/policies`, `/access/v1`, `/grants`, `/idp`, `/audit`, `/healthz`); chi is stdlib-compatible with zero reflection magic. Request/response bodies are explicit structs in `internal/contract` with hand-written validation functions that mirror the current zod schemas rule-for-rule (regexes, length limits, enum values), verified by the shared fixture corpus.

**D3. Database access.** `pgx/v5` with `pgxpool`; hand-written repository functions per module (the schema is 7 tables — a codegen layer is not justified). All timestamps keep `timestamptz` semantics; `audit_log.occurred_at` keeps ms precision (precision 3) and keyset pagination semantics.

**D4. OpenFGA access.** Official `github.com/openfga/go-sdk`. The `Gateway` interface is ported as-is (bootstrap with env/store-id adoption + warning on missing env store, ping via ListStores, check, exhaustive paginated read, transaction-mode write, writeAuthorizationModel). Error classification (`isTransientApiError`, `classifyWriteError`) is ported with identical decision rules, adapted to the Go SDK's error types: HTTP status ≥500 or 429 ⇒ transient; an SDK API error that carries **no HTTP response** ⇒ transient; recognized network-level errors (connection refused/reset, timeout, DNS) ⇒ transient; the TS message-pattern fallback is kept for parity; **any other status-less error is deterministically non-transient** (anti-retry-loop rule, as in TS). Deterministic 4xx is never transient. Idempotent absorption only on `write_failed_due_to_invalid_input` + op-specific exact patterns.

**D5. Conditions/CEL.** `condition-to-cel` is ported to Go; generated CEL for the pure-CEL subset (timestamp/string/int/double/bool comparisons) is additionally compiled and type-checked with `google/cel-go` in tests (an assurance upgrade over TS string-building). `ipaddress`/`in_cidr` leaves use OpenFGA's custom CEL extensions that plain cel-go does not know, so they are covered by golden fixtures plus real-OpenFGA validation at the E2E stage (the demo publishes a condition through `WriteAuthorizationModel`, which is authoritative). Runtime behavior is unchanged: OpenFGA evaluates the CEL.

**D6. Auth.** Same three-tier semantics: admin token (env) and service tokens (sha256 stored, constant-time comparison), `401` for auth failure, `403` for role mismatch, infrastructure errors propagate as `500` (never masked as 401). Token generation keeps 32-byte random base64url plaintext shown once.

**D7. Startup/operational semantics.** Same as today: server listens immediately; bootstrap (migrate → FGA store bootstrap) runs with transient-error retry/backoff in the background; `/healthz` reports `degraded`/503 until `db+openfga+storeReady`; non-recoverable bootstrap errors exit(1). Graceful shutdown on SIGTERM/SIGINT (http server shutdown with timeout) is added — required for container orchestration.

**D8. Testing & coverage.** Hard requirement: **total statement coverage ≥95%** over the Go module, enforced by a CI script. Mechanics are explicit so the gate is meaningful: coverage is measured as `go test ./... -coverpkg=./...` (cross-package attribution; `cmd/*` packages included), every binary is a thin `main()` delegating to a testable `Run(deps)` function, integration tests are **skip-if-unavailable locally but mandatory in CI** (the CI gate mode sets `LAZYFGA_TEST_INTEGRATION=1`, under which a missing dependency fails the test instead of skipping), the gate script runs under `set -euo pipefail`, and the per-package `go tool cover -func` report is printed on every CI run. Test architecture per layer:

- contract/compiler/idp-engine: pure table-driven tests + fixture corpus (bulk of coverage).
- repositories + migrator: integration tests against real Postgres (CI service container; locally via compose).
- OpenFGA gateway + write-error: `httptest` fake OpenFGA server steering success/4xx/5xx/network-cut paths, plus a smoke test against real OpenFGA in the E2E stage.
- HTTP handlers: `httptest` with fake Gateway/repos covering every status-code branch.
- app bootstrap: `Run()` tested with fakes for retry/degraded/fatal paths; `main()` stays a thin wrapper.

**D9. Cutover strategy.** The Go backend is developed at `apps/api-go` while the TS backend remains runnable. Before the swap, LFGA-27 runs a **dual-backend parity rehearsal**: the same scripted HTTP contract replay (success + error cases for every route) is executed against the TS backend and the Go backend, and responses are diffed modulo the §4.4 approved deviations; additionally a TS-created Postgres volume is booted under the Go api to rehearse migration adoption, and the demo scenario runs end-to-end on both. Only after the rehearsal is green does LFGA-27 perform the atomic swap in one commit: delete TS `apps/api`, move `apps/api-go` → `apps/api`, point Dockerfile/compose/docs/CI at Go, update `CLAUDE.md` file-structure and ARCHITECTURE.md stack lines. Same port (8787) and identical env var names make the swap invisible to web and compose users.

**D10. Commit/process discipline.** Each sub-proposal follows Proposal-Driven Development (pre-inspection → TDD implementation → Claude+Codex parallel review → fix-all → tests → conventional commit, SSH-signed). Proposals LFGA-23 ∥ LFGA-24 can be implemented in parallel (disjoint: infrastructure vs pure logic), then LFGA-25 ∥ LFGA-26 (disjoint module sets), then LFGA-27 (cutover, sequential).

### 4.4 Approved Deviations (exhaustive)

Everything not listed here must be byte/status-identical to the TS backend. Each deviation below is deliberate, tested, and called out in the owning sub-proposal:

1. **Zod issue detail format** (LFGA-24/25): 422 shape-validation responses keep the same status and top-level shape (`{"error":"invalid IR shape","issues":[...]}`), but each issue is `{path, message}` instead of zod's internal issue object. Rationale: zod internals are not a stable contract; the web UI only displays these strings. LFGA-27 verifies the web publish-error flow renders correctly.
2. **Invalid UUID text → 404** (LFGA-25/26): TS forwards invalid uuid text to Postgres and surfaces a 500; Go maps Postgres's own rejection (SQLSTATE 22P02) to not-found for `/model/versions/:id`, `/tokens/:id`, `/idp/connections/:id` (+its rules route), `/idp/rules/:ruleId`, and the id-shaped `/model/diff` `from`/`to` query params. Because the mapping delegates to the PG uuid parser, PG-accepted forms (un-hyphenated, braced) resolve normally, exactly as they did in TS. Strictly-better error mapping; the web never emits malformed ids.
3. **Audit `from`/`to` accepted date formats and cursor strictness** (LFGA-26): TS accepts anything JS `new Date(...)` parses and Node's lenient base64 decoding for cursors; Go accepts RFC3339 (with or without fractional seconds, truncated to ms like JS Date) and `YYYY-MM-DD` (UTC midnight), returning the same 400 message otherwise, and requires canonical unpadded base64url + RFC3339 inside hand-crafted cursors. Server-issued cursors round-trip identically on both sides.
4. **Additive hardening** (LFGA-23/27): migrator advisory lock (multi-instance safety; includes moving Drizzle's last-applied `SELECT` inside the locked transaction — outcome-identical for single instances), graceful shutdown on SIGTERM/SIGINT, compose api healthcheck, compose refusing to start without an explicit `ADMIN_TOKEN` (`${ADMIN_TOKEN:?...}`) plus a loud startup warning when the value is empty or the known placeholder. None of these change request/response behavior.
5. **`PORT` env edge cases** (LFGA-23): `PORT=""` falls back to 8787 (TS `Number("")` = 0 would bind a random port — a footgun, not a behavior worth preserving); a non-numeric `PORT` fails startup with an error (TS equivalently fails to boot with NaN). Unset behaves identically (8787).
6. **Framework default bodies are matched, not deviated**: unknown routes return Hono's default `404 Not Found` (text/plain) and unhandled infrastructure errors return Hono's default `Internal Server Error` (text/plain), reproduced exactly in the Go router (trailing-slash and unknown paths inside guarded groups flow through the auth guard first and then fall through to the Hono 404 body, matching measured TS behavior; path params are percent-decoded) so the LFGA-27 contract replay sees no framework-default diffs.
7. **Runtime error free-text is not byte-identical** (LFGA-25/26): fields whose values embed an SDK/driver error message — publish 502 `{openfga}` / 500 `{db}` detail values, grant 502/400 detail strings, and the `error` values inside `pdp.evaluate.openfga_error`/`idp.tuple.error` audit data — necessarily differ between the JS and Go SDKs. Status codes, codes, and all other fields are identical; the LFGA-27 replay masks these free-text values. The pdp array-`context` case is reproduced observably (audit + 500) without a live OpenFGA rejection, with only this masked error text differing.
8. **`PUT /idp/rules/:ruleId` with `tupleTemplate: null` → 422** (LFGA-26): TS crashes on `null.user` and surfaces Hono's default 500; Go returns the 422 shape-validation error. Replicating a crash is not production-ready behavior; documented instead.
9. **Webhook signature Node-lenience edges** (LFGA-26, theoretical): invalid-UTF-8 bodies — TS lossily decodes before HMAC (valid raw-byte signatures fail → 401) while Go signs raw bytes (signature verifies, then JSON parse fails → 400); Go's base64 secret/signature decoding rejects exotic inputs Node lenient-decodes (both still fail verification). Unreachable with the shipped presets and real IdPs; documented for the replay.
10. **Stored/echoed JSON normalization**: values persisted as `jsonb` are echoed back in Postgres-normalized key order compacted like TS (`json.Compact`), and the publish path stores the canonical serialization of the decoded IR exactly as TS stores zod's `parsed.data`. Residual theoretical divergence exists only for audit `data` containing exotic numerics (non-shortest literals, >2^53 integers, numeric-like object keys) — never produced by this system's writers.
11. **Duplicate IdP provider returns the documented 409** (LFGA-26): the TS backend's 409 detection string-sniffs the error message, and the dependabot bump to `drizzle-orm` 0.45.x wraps driver errors in `DrizzleQueryError` ("Failed query: …"), so the shipped TS build actually returns Hono's 500 on a duplicate provider (verified in the dual-backend rehearsal). Go detects SQLSTATE 23505 and returns the contractually documented `409 {"error":"provider already exists"}` — a TS regression fix, not a drift.

## 5. API Design

### 5-1. New / Modified

**No externally visible API changes.** The complete route surface below is ported verbatim (method, path, auth role, request/response JSON, status codes). The normative behavior spec for each route is the current TS implementation plus its proposal (LFGA-7/8/9/10/11/14/15/17/20/21); each sub-proposal carries the exact route list it owns.

| Mount        | Routes (summary)                                                             | Auth              | Sub-proposal |
| ------------ | ---------------------------------------------------------------------------- | ----------------- | ------------ |
| `/healthz`   | GET liveness/readiness (db, openfga, storeReady)                              | public            | LFGA-23      |
| `/model`     | validate, publish, versions list/get, diff, current                           | admin             | LFGA-25      |
| `/policies`  | CRUD for named policies                                                       | admin             | LFGA-25      |
| `/access/v1` | AuthZEN evaluation (decision + reason context)                                | service \| admin  | LFGA-25      |
| `/grants`    | structural grant/revoke/list                                                  | admin             | LFGA-25      |
| `/tokens`    | service token issue/list/revoke                                               | admin             | LFGA-26      |
| `/audit`     | audit log query (keyset pagination)                                           | admin             | LFGA-26      |
| `/idp`       | webhook receive (HMAC signature auth) + connection/mapping-rule CRUD          | signature / admin | LFGA-26      |

Internal (non-HTTP) surfaces ported: compiler API (`compileIrToDsl`/`conditionToCel` — Go: `compiler.CompileIRToDSL`/`compiler.ConditionToCel`; `dsl-to-ir`/`coverage` stay TS/web-only), contract validation functions, idp signature/extraction/mapping engines, demo/seed CLIs.

### 5-2. Error Handling

Unchanged from the current backend, now guaranteed by handler tests per branch:

| Status Code | Description                                                                    |
| ----------- | ------------------------------------------------------------------------------ |
| 400         | validation failure (contract violation, unsupported IR/DSL, bad condition)     |
| 401         | missing/invalid bearer token; invalid webhook signature                        |
| 403         | authenticated but role not allowed                                             |
| 404         | unknown resource (policy, version, connection, rule…)                          |
| 409         | conflict (duplicate policy key, …) where the TS backend returns it today       |
| 422         | semantically invalid but well-formed input where the TS backend returns it     |
| 502         | transient OpenFGA failure (after classification), surfaced as upstream error   |
| 503         | `/healthz` degraded (dependency down or store not ready)                       |
| 500         | unexpected internal error (infrastructure failures are never masked as 401)    |

(The authoritative per-route matrix is enumerated in each sub-proposal; parity is verified by porting the existing route tests and extending them.)

## 6. Implementation Plan

### 6-1. Milestones

Each phase = one sub-proposal, implemented under Proposal-Driven Development with mandatory Claude+Codex parallel review and an SSH-signed conventional commit per phase. ∥ marks phases implemented in parallel.

| Phase             | Task                                                                                                              | Estimated Duration | Owner        |
| ----------------- | ----------------------------------------------------------------------------------------------------------------- | ------------------ | ------------ |
| Phase 1 (LFGA-23) | Go foundation: module scaffold, config, slog, chi server, auth middleware, pgx + drizzle-compatible migrator, OpenFGA gateway + write-error, healthz, bootstrap/degraded mode | 1 day              | Seonguk Moon |
| Phase 1′ (LFGA-24) ∥ | Contracts + compiler port: `internal/contract` (model/ident/condition/authzen/policy/reason/audit/grant + validation), `internal/compiler` (ir-to-dsl, condition-to-cel), golden fixture corpus + TS/Go parity harness | 1 day              | Seonguk Moon |
| Phase 2 (LFGA-25) | Core modules: model (validate/publish/versions/diff), policy CRUD, pdp evaluate + reason engine, grants            | 1 day              | Seonguk Moon |
| Phase 2′ (LFGA-26) ∥ | Platform modules: service tokens, audit log, idp framework (signature, extraction, presets, mapping engine, webhook + CRUD) | 1 day              | Seonguk Moon |
| Phase 3 (LFGA-27) | Cutover: demo/seed CLIs, Dockerfile, compose, CI rewrite + coverage gate + parity job, docs, TS api removal, E2E (compose up + demo + web against Go) | 1 day              | Seonguk Moon |

Dependency rule: Phase 2/2′ require Phase 1 **and** 1′ merged. Phase 3 requires all prior phases. Within a wave, parallel implementations run in isolated worktrees and merge sequentially (rebase, full test run before each merge).

### 6-2. Dependencies

Go dependencies (all latest stable at implementation time, pinned in `go.mod`):

- `github.com/go-chi/chi/v5` — HTTP router.
- `github.com/jackc/pgx/v5` — Postgres driver + pool.
- `github.com/openfga/go-sdk` — official OpenFGA client.
- `github.com/openfga/language` — official DSL↔JSON transformer (Go package).
- `github.com/google/cel-go` — CEL compile/typecheck in tests (D5).
- `github.com/stretchr/testify` — test assertions (test-only).
- Toolchain: Go 1.25 (required by `openfga/language/pkg/go` v0.3.0; verified locally — the 1.24 toolchain auto-provisions 1.25 via GOTOOLCHAIN), golangci-lint, existing Docker/compose. CI: `actions/setup-go`, `golangci/golangci-lint-action`, service containers `postgres:16-alpine`, `openfga/openfga:v1.18.0`.

External constraints: none beyond OpenFGA/Postgres already in use. No other teams involved.

## 7. References

- [CONCEPT.md](../CONCEPT.md), [ARCHITECTURE.md](../ARCHITECTURE.md), [ROADMAP.md](../ROADMAP.md) — product concept, current architecture, milestone history.
- Normative behavior specs being ported: `proposals/lazyfga-7…21` (publish, policy store, PDP/AuthZEN, tokens, reason engine, condition→CEL, idp webhook framework, audit, permission management).
- Sub-proposals governed by this plan: LFGA-23 (foundation), LFGA-24 (contracts+compiler), LFGA-25 (core modules), LFGA-26 (platform modules), LFGA-27 (cutover & CI).
- OpenFGA Go SDK — https://github.com/openfga/go-sdk
- OpenFGA language (Go transformer) — https://github.com/openfga/language
- cel-go — https://github.com/google/cel-go
- OpenID AuthZEN 1.0 — https://openid.github.io/authzen/
- chi router — https://github.com/go-chi/chi ; pgx — https://github.com/jackc/pgx
