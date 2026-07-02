# Go Foundation: Runtime, DB, OpenFGA Gateway - Spec Proposal

| Item      | Detail       |
| --------- | ------------ |
| Author    | Seonguk Moon |
| Created   | 2026-07-02   |
| Status    | **Implemented** |
| Reviewers | Claude (review agent), Codex (gpt-5.5) |

---

## 1. Summary

Build the Go backend skeleton at `apps/api-go` that every later migration proposal plugs into: Go module + entrypoint, env config, structured logging, chi HTTP server with the auth middleware, pgx database layer with a **drizzle-compatible migrator** (adopts existing databases untouched), the OpenFGA `Gateway` (bootstrap/check/read/write/writeAuthorizationModel) with ported error classification, `/healthz`, and the listen-immediately/bootstrap-in-background/degraded-mode startup semantics. Everything lands with tests at ≥95% statement coverage for the packages introduced here.

Governed by LFGA-22 (master plan). Runs in parallel with LFGA-24 (disjoint code).

## 2. Background & Motivation

- LFGA-22 fixes the target architecture; this proposal delivers its foundation layer so module ports (LFGA-25/26) only add handlers/services/repos, not plumbing.
- The TS foundation being ported consists of: `src/index.ts` (bootstrap, retry, degraded mode, healthz, route mounting), `src/config.ts`, `src/middleware/auth.ts`, `src/db/{client,migrate,schema,migrations}`, `src/openfga/{gateway,write-error,instance}`.
- The riskiest continuity point is DB migration bookkeeping: existing volumes were migrated by Drizzle (`drizzle.__drizzle_migrations`). A Go migrator that cannot adopt that state would break every existing deployment — unacceptable for a production-ready cutover.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `apps/api-go` Go module (`github.com/antegral-constellation/lazyfga/api`), `cmd/lazyfga-api` entrypoint, `internal/app.Run()` orchestration.
- [ ] `internal/config`: env parsing with the exact same variables and defaults as `config.ts` (`PORT`=8787, `DATABASE_URL`=`postgres://lazyfga:lazyfga@localhost:5432/lazyfga`, `OPENFGA_API_URL`=`http://localhost:8080`, `LAZYFGA_STORE_ID` optional, `ADMIN_TOKEN` default empty). Startup logs a loud warning when `ADMIN_TOKEN` is empty **or equals the known placeholder** `changeme-admin-token` (LFGA-22 §4.4-4).
- [ ] `internal/db`: pgxpool client, `Ping`, and a migrator that embeds `0000`–`0006` SQL + journal and reads/writes `drizzle.__drizzle_migrations` with Drizzle's exact semantics (see 4.3).
- [ ] `internal/openfga`: `Gateway` interface identical in behavior to the TS one (store bootstrap precedence env → stored → create, warning on missing env store; ping via ListStores; check with optional context and authorization-model pin; read with exhaustive continuation-token pagination and condition passthrough; transaction-mode write; writeAuthorizationModel) + `write-error` classification ported rule-for-rule.
- [ ] `internal/httpx`: chi router assembly, JSON helpers, request logging, panic recovery, a **body-limit middleware** (used by LFGA-26's webhook route; enforced before any handler logic), and the auth middleware: bearer parsing, admin token constant-time compare, service-token sha256 lookup with best-effort `last_used_at` touch, 401/403 responses, infrastructure errors → 500 (never 401).
- [ ] `internal/jsontime`: timestamp type serializing exactly like JS `Date.toISOString()` (UTC, millisecond precision, trailing `Z`) — used by every exposed timestamp across LFGA-25/26.
- [ ] `/healthz` returning `{status, version, db, openfga, storeReady}` with 200/503, no infra identifiers leaked.
- [ ] Startup semantics: listen immediately; background bootstrap = migrate → FGA bootstrap with transient-only backoff retry (attempts=8, `min(1000*i,5000)`ms); transient exhaustion → keep serving degraded; non-transient → log + exit(1). Plus graceful shutdown on SIGTERM/SIGINT.
- [ ] `service_token` repository (needed by the middleware; full token CRUD routes are LFGA-26).
- [ ] `AuditRecorder`: the interface **and** the final DB-backed write implementation (`Record(action, data, actor)` → async `audit_log` insert, panic-recovered, errors logged, never blocks or fails the caller) plus `PrincipalActor` mapping. LFGA-25/26 only consume this; LFGA-26 owns the read/query side. (Ownership is fixed here so the parallel wave LFGA-25 ∥ LFGA-26 stays conflict-free.)
- [ ] Tests: ≥95% statement coverage across all packages introduced here (fake OpenFGA `httptest` server; real Postgres for db/migrator/repo tests; handler and bootstrap tests with fakes).

### 3.2 Non-Goals

- [ ] No business module routes (`/model`, `/policies`, `/access/v1`, `/grants`, `/tokens`, `/audit`, `/idp`) — LFGA-25/26.
- [ ] No contract/compiler code — LFGA-24.
- [ ] No Dockerfile/CI/compose changes — LFGA-27 (during development the Go app runs via `go run` against the existing compose stack).
- [ ] No new migrations — only embedding the existing seven.

## 4. Technical Design

### 4.1 Architecture Overview

```
cmd/lazyfga-api/main.go        → app.Run(ctx) (thin, os.Exit on error)
internal/app                   → config.Load → db.Connect → httpx.NewServer(deps) → listen
                                 → go bootstrap(): db.Migrate → gateway.Bootstrap (withRetry)
                                 → signal handling → server.Shutdown(grace 10s)
internal/httpx                 → chi.Router: /healthz + mount points added by LFGA-25/26
                                 middleware: RequestLogger(slog) → Recover → per-group RequireRole
internal/db                    → pgxpool; Migrator{embed.FS sql + journal}; repo helpers
internal/openfga               → Gateway interface; impl on openfga/go-sdk; writeerror pkg
internal/config                → Config struct + Load() from env
```

Dependency injection is plain constructor wiring (no DI framework). `Gateway`, repositories, and the clock are interfaces/types owned by their consumers so handlers and `app.Run` are testable with fakes.

### 4.2 Data Model Changes

None. The migrator embeds the existing `apps/api/src/db/migrations/*.sql` and `meta/_journal.json` files (copied verbatim into `internal/db/migrations/`) and reproduces the schema byte-identically on fresh databases.

### 4.3 Core Logic

**Drizzle-compatible migrator.** Reimplements `drizzle-orm/postgres-js/migrator` semantics exactly:

1. `CREATE SCHEMA IF NOT EXISTS drizzle`; `CREATE TABLE IF NOT EXISTS drizzle.__drizzle_migrations (id SERIAL PRIMARY KEY, hash text NOT NULL, created_at bigint)`.
2. Read journal entries `(idx, when, tag)`; for each SQL file compute sha256 hex of the file content (Drizzle's hash of the raw migration text, matching its stored format).
3. Open **one transaction** and, as its first statement, take `pg_advisory_xact_lock` on a fixed key (concurrency guard so parallel instances serialize; additive hardening, single-instance behavior unchanged).
4. Inside that same transaction: `SELECT id, hash, created_at FROM drizzle.__drizzle_migrations ORDER BY created_at DESC LIMIT 1` → `last`; then for each journal entry in order, if `last` is absent or `entry.when > last.created_at`, execute the file's statements (split on Drizzle's `--> statement-breakpoint` marker) and `INSERT (hash, created_at) VALUES (sha256(file), entry.when)`. Commit releases the lock.
5. Result-equivalence note: Drizzle reads `last` outside its apply transaction; moving the read inside the lock-holding transaction is strictly safer and produces identical outcomes for the single-instance case (asserted by the adoption/no-op integration tests).

Adoption invariant: a DB whose `__drizzle_migrations` already lists `when` values for 0000–0006 gets zero statements executed. Verified by an integration test that first applies migrations, then re-runs the migrator and asserts no-op, and by a test that replays a captured Drizzle-written bookkeeping table.

**Gateway bootstrap.** Port of `gateway.ts` `bootstrap()`:

```
candidate = envStoreId ?? loadStoredStoreId()
if candidate exists in OpenFGA (GetStore ok)      → use it
else if candidate came from env                   → slog.Warn(skew risk) and fall through
if no store chosen                                → CreateStore(name="lazyfga")
persistStoreId(storeId); bind client to store
```

`loadStoredStoreId`/`persistStoreId` stay callbacks (the openfga package remains DB-independent, as in TS). `instance_config` upsert keeps the singleton row (`id='singleton'`, `updated_at` refresh).

**Error classification (`writeerror`).** Port with Go-SDK error mapping, reproducing the TS decision table exactly (`write-error.ts`):

1. HTTP status ≥500 or 429 ⇒ transient (Go SDK: `FgaApiInternalError`, `FgaApiRateLimitExceededError`, or any SDK error whose response status satisfies the rule).
2. No HTTP status present: transient **only** when the error is (a) an SDK API error that carries no HTTP response (network stage — TS `FgaApiError` with `statusCode === undefined`; Go: SDK error wrapping `*url.Error`/`net.Error`), or (b) a recognized network error class (connection refused/reset, timeout, DNS — Go `errors.As` on `net.Error`/`*net.OpError`/syscall errnos), or (c) the parity message-pattern fallback matches ("fetch failed"/"network"/"timeout"/"econnrefused" analogues).
3. **Any other status-less error is deterministically non-transient** — the TS anti-infinite-retry rule ("정체불명 + status 없음 → 결정적") is preserved.
4. Deterministic 4xx is never transient regardless of message text.

`ClassifyWriteError(err, op)` absorbs as idempotent only when the invalid-input signal is present **and** the op-specific pattern matches (`write`: "already exists"/"duplicate"; `delete`: "cannot delete"/"does not exist"). The invalid-input signal reproduces both TS detection paths: the SDK error's API code equals `write_failed_due_to_invalid_input` (Go SDK: `FgaApiValidationError` response code) **or** the error message contains that string (TS message fallback kept). Gateway `Write` always uses the SDK's single-transaction write (non-batch), matching TS client behavior.

Version pin note: these semantics (and the migrator's) are ported from the lockfile-resolved `drizzle-orm@0.38.4` / `@openfga/sdk@0.9.x` behavior actually running today (`package.json` ranges are newer but unresolved); the ported behavior is frozen by our own tests from here on.

**Auth middleware.** Port of `middleware/auth.ts`:

- `Bearer\s+(.+)` case-insensitive parse; missing/blank → 401 `{"error":"unauthorized"}`.
- Admin: `subtle.ConstantTimeCompare(sha256(token), sha256(config.AdminToken))` (only when AdminToken non-empty).
- Else service-token lookup by sha256 hex against `service_token` where `revoked_at IS NULL`; hit → principal `{role: service, tokenID}` + async best-effort `last_used_at` update (errors swallowed).
- Miss → 401; repository infrastructure error → 500 (propagated, not 401). Role guard returns 403 with `{"error":"forbidden"}`.
- Principal is carried in `context.Context` (typed key), the Go analogue of Hono's `c.set("principal")`.

**Startup/degraded.** `app.Run` ports `index.ts`: `isTransient` (connection-refused/reset/timeout/DNS classes via `errors.As` on `net`/`syscall` errors — no message sniffing where a typed check exists, message fallback kept for parity), `withRetry(fn, label, attempts=8)` with `min(i*1s, 5s)` sleeps, `storeReady` flag flipped after successful bootstrap, healthz 503 until then, fatal path `os.Exit(1)`.

## 5. API Design

### 5-1. New / Modified

HTTP: only `/healthz` (public):

```
GET /healthz → 200 {"status":"ok","version":"0.0.0","db":"up","openfga":"up","storeReady":true}
             → 503 {"status":"degraded", ...}   # any dependency down or store not ready
```

Key internal signatures (English doc comments in real code):

```go
// Load reads configuration from the environment, applying the same
// defaults as the TS backend's config.ts.
func config.Load() Config

// Migrate applies embedded migrations using drizzle-compatible bookkeeping.
// It is idempotent and safe under concurrent startup (advisory lock).
func (m *db.Migrator) Migrate(ctx context.Context, pool *pgxpool.Pool) error

// Gateway is the single OpenFGA entry point bound to one store after Bootstrap.
type openfga.Gateway interface {
    Bootstrap(ctx context.Context, opts BootstrapOptions) (storeID string, err error)
    StoreID() (string, error)          // error until bootstrapped
    Ping(ctx context.Context) bool
    Check(ctx context.Context, in CheckInput, opts ...CheckOption) (allowed bool, err error)
    Read(ctx context.Context, in ReadInput) ([]ReadTuple, error)   // exhaustive pagination
    Write(ctx context.Context, in WriteInput, opts ...WriteOption) error
    WriteAuthorizationModel(ctx context.Context, model authmodel.JSON) (modelID string, err error)
}

// IsTransientAPIError reports whether an OpenFGA/SDK error is retryable:
// HTTP >=500 or 429; or status-less AND (SDK network-stage error, recognized
// net error class, or the parity message patterns). Unknown status-less
// errors are non-transient (anti-retry-loop rule).
func writeerror.IsTransientAPIError(err error) bool

// ClassifyWriteError classifies write/delete failures into
// {Idempotent, Transient} with op-exact absorption patterns.
func writeerror.ClassifyWriteError(err error, op Op) Classification

// RequireRole guards a route group; sets Principal into the request context.
func httpx.RequireRole(auth Authenticator, roles ...Role) func(http.Handler) http.Handler
```

### 5-2. Error Handling

| Case                                                | Result                                   |
| --------------------------------------------------- | ---------------------------------------- |
| Missing/invalid bearer token                        | 401 `{"error":"unauthorized"}`           |
| Valid token, insufficient role                      | 403 `{"error":"forbidden"}`              |
| Token lookup infra failure (DB down)                | 500 (propagated, never masked as 401)    |
| Healthz with dependency down / store not ready      | 503 `status:"degraded"`                  |
| Bootstrap transient failure after 8 attempts        | keep serving; healthz stays 503          |
| Bootstrap non-transient failure (bad config/perms)  | log fatal + exit(1)                      |
| Panic in handler                                    | 500 via recover middleware, stack logged |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                           | Estimated Duration | Owner        |
| ------- | ---------------------------------------------------------------------------------------------- | ------------------ | ------------ |
| Phase 1 | Module scaffold, config, slog, chi server shell, healthz (static parts), JSON/error helpers     | 0.25 day           | Seonguk Moon |
| Phase 2 | db: pgxpool + drizzle-compatible migrator (embedded SQL/journal) + integration tests            | 0.25 day           | Seonguk Moon |
| Phase 3 | openfga: Gateway impl + writeerror + fake-server tests; instance_config persistence callbacks   | 0.25 day           | Seonguk Moon |
| Phase 4 | auth middleware + service_token repo; app.Run bootstrap/degraded/shutdown wiring + tests        | 0.25 day           | Seonguk Moon |

One SSH-signed conventional commit at the end (or per phase if review demands splitting), Claude+Codex parallel review before commit.

### 6-2. Dependencies

- `github.com/go-chi/chi/v5`, `github.com/jackc/pgx/v5`, `github.com/openfga/go-sdk`, `github.com/stretchr/testify` (test).
- Real Postgres + OpenFGA from the existing `docker-compose.yml` for integration tests.
- LFGA-22 decisions D2–D4, D6–D8.

## 7. References

- TS sources being ported: `apps/api/src/index.ts`, `config.ts`, `middleware/auth.ts`, `db/client.ts`, `db/migrate.ts`, `db/schema.ts`, `db/migrations/*`, `openfga/gateway.ts`, `openfga/write-error.ts`.
- [LFGA-22 master plan](lazyfga-22-go-migration-master-plan.md); original specs `lazyfga-1`, `lazyfga-10`, `lazyfga-15` (hardening), `lazyfga-20` (pagination/idempotency semantics).
- Drizzle postgres-js migrator semantics — https://github.com/drizzle-team/drizzle-orm (bookkeeping table `drizzle.__drizzle_migrations`).
- OpenFGA Go SDK — https://github.com/openfga/go-sdk
