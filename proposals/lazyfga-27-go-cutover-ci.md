# Go Cutover: CLIs, Docker, CI, Docs - Spec Proposal

| Item      | Detail                                 |
| --------- | -------------------------------------- |
| Author    | Seonguk Moon                           |
| Created   | 2026-07-02                             |
| Status    | **Implemented**                        |
| Reviewers | Claude (review agent), Codex (gpt-5.5) |

---

## 1. Summary

Finish the migration and make it shippable: port the operational scripts to Go CLIs (`cmd/demo`, `cmd/seed-zitadel-rules`, zitadel-sign helper), replace the Bun Dockerfile with a multi-stage Go build, swap `apps/api-go` into `apps/api` (deleting the TS backend), rewrite CI for a Go lane with an enforced **≥95% coverage gate** plus a Node lane (web + packages) and the compiler parity job, update all documentation (README, docs, CLAUDE.md file structure, ARCHITECTURE.md stack), and verify immediate deployability end-to-end: `docker compose up` → healthy stack → demo scenario ALLOW with reason → web UI E2E against the Go backend.

Governed by LFGA-22 (D9/D10). Depends on LFGA-23/24/25/26 all merged. Sequential (no parallel counterpart).

## 2. Background & Motivation

- After LFGA-23–26 the Go backend is functionally complete but the repo still builds/ships the TS backend. The goal requires 100% Go **and** production readiness: a clean single cutover commit avoids a long window where two backends drift.
- CI today (`ci.yml`) is Node/Bun-only (turbo lint/typecheck/build/test + prettier + license audit). It must build and gate the Go backend, including the coverage requirement, or the 95% bar is aspirational rather than enforced.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] Go CLIs replacing `apps/api/scripts`: `cmd/demo` (subcommands `run`/`reset` porting `demo/run.ts` + `demo/reset.ts`), `cmd/seed-zitadel-rules`, `internal/zitadelsign` (HMAC helper used by demo + tests).
- [ ] Dockerfile: multi-stage (`golang:1.25-alpine` build, CGO_ENABLED=0, `-trimpath -ldflags "-s -w"`; runtime `gcr.io/distroless/static-debian12:nonroot`), embedded migrations, same port 8787, same env vars. `docker-compose.yml` keeps the same service topology with two hardenings (LFGA-22 §4.4-4): the api service gains a healthcheck via a `healthcheck` mode of the binary (`["CMD","/lazyfga-api","healthcheck"]` — self HTTP GET of `/healthz`, needed because distroless has no shell/curl), and `ADMIN_TOKEN` becomes required (`${ADMIN_TOKEN:?set ADMIN_TOKEN in .env}` instead of a baked-in default) so a deployment can no longer ship the known placeholder implicitly.
- [ ] Cutover swap in one commit: delete TS `apps/api`, move `apps/api-go` → `apps/api`, drop `@lazyfga/api` from the pnpm workspace (Go app has no package.json), keep web/`packages` untouched.
- [ ] CI rewrite (`.github/workflows/ci.yml`):
  - **go job**: setup-go (1.25, cache), `go vet`, golangci-lint, `go build ./...`, then the coverage gate script (below) with service containers `postgres:16-alpine` + `openfga/openfga:v1.18.0`. Coverage mechanics per LFGA-22 D8: `-coverpkg=./...` (cross-package attribution, `cmd/*` included; every binary is a thin `main()` over a testable `Run`), CI sets `LAZYFGA_TEST_INTEGRATION=1` so dependency-backed tests **fail rather than skip** when a dependency is missing, per-package `go tool cover -func` report printed, gate fails below **95.0%** total statements.
  - **node job**: unchanged in spirit — pnpm + turbo `lint typecheck build test` over web/packages + prettier check (bun still required for package tests).
  - **parity job**: runs the TS parity corpus tests and the Go parity corpus tests (both must pass on the same corpus files; job exists to make drift failures legible even when unit jobs are green).
  - license-audit job kept; extended with `go mod verify` + informational Go module license listing.
- [ ] Docs: README quickstart (Go run instructions, compose flow, demo command), `docs/getting-started.md`, `docs/api.md` (only run/dev command changes — HTTP API is unchanged), `ARCHITECTURE.md` stack line + compiler-sharing note (D1 parity corpus), `CLAUDE.md` file-structure tree.
- [ ] Pre-swap **dual-backend parity rehearsal** (LFGA-22 D9): a scripted HTTP contract replay (success + error cases for every route, incl. auth failures and malformed inputs) runs against the TS backend and the Go backend on fresh identical stacks; responses are diffed modulo the LFGA-22 §4.4 approved deviations. The demo scenario runs on both. A TS-created Postgres volume is then booted under the Go api (adoption rehearsal). The swap commit is allowed only after all three artifacts are green.
- [ ] E2E acceptance (all must pass before the goal is declared done):
  1. `docker compose up --build` from scratch → `/healthz` 200 `ok` and the api container reports healthy.
  2. `go run ./cmd/demo run` → webhook applied, structural tuples seeded, evaluation `ALLOW` with witnessing-path reason text.
  3. Existing-volume adoption: a DB volume created by the TS backend boots the Go api with zero re-migration (migrator integration test + the rehearsal above).
  4. Web UI E2E via Chrome DevTools MCP against the Go backend (canvas → publish → playground evaluate → audit view, plus the publish-validation-error display exercising deviation §4.4-1).
  5. Coverage gate green: `-coverpkg=./...` total ≥95% with mandatory integration tests.

### 3.2 Non-Goals

- [ ] No web build/deploy pipeline changes beyond what the api swap requires (web is dev-served / built as today).
- [ ] No new CI features unrelated to the migration (codeql.yml / trufflehog.yml untouched).
- [ ] No Kubernetes/helm work — compose remains the shipped self-host path (as today).
- [ ] No version bump of OpenFGA/Postgres images.

## 4. Technical Design

### 4.1 Architecture Overview

Final tree (delta from LFGA-22 §4.1: `apps/api-go` becomes `apps/api`):

```
apps/api/            # Go module (was api-go); cmd/{lazyfga-api,demo,seed-zitadel-rules}, internal/...
apps/web/            # unchanged
packages/{shared,compiler}/  # TS, web-only consumers + parity corpus
.github/workflows/ci.yml     # go + node + parity + license jobs
docker-compose.yml           # api service builds the Go Dockerfile
apps/api/Dockerfile          # multi-stage Go build
```

### 4.2 Data Model Changes

None. The Go migrator (LFGA-23) already owns schema lifecycle; cutover adds no migrations.

### 4.3 Core Logic

**Demo CLI (`cmd/demo run`)** — for coverage (LFGA-22 D8) all CLI logic lives in an `internal/democli` package whose `Run(deps)` takes the API base client, gateway, and DB access as injectable deps (tested against `httptest` fakes; `cmd/demo/main.go` stays a thin wrapper). It ports `demo/run.ts` step-for-step: healthz preflight; compose the demo IR by loading the `doc-folder-team.ir.json` fixture (embedded copy, source of truth remains `packages/shared/src/__fixtures__/` — the file is copied at build time via `go:embed` after a parity checksum test asserts the embedded copy equals the fixture) and applying the same two edits in Go (append `non_expired` condition; set `document.owner.assignableBy[0].condition`); publish; seed policy `can-read-doc` (409-tolerant); idempotent zitadel connection+rule seeding (PUT secret when existing, clear-then-insert rules); signed webhook replay (seconds timestamp, `t=...,v1=...`); structural tuples via the Gateway bound to the stored store id (read from `instance_config` via pgx — same env contract: `API_BASE`, `ADMIN_TOKEN`, `ZITADEL_SIGNING_SECRET`, `OPENFGA_API_URL`, `DATABASE_URL`); evaluate with `options.reason` and print decision + reason text. `cmd/demo reset` ports `reset.ts` (delete policy, delete zitadel connection, delete the three demo tuples idempotently). `cmd/seed-zitadel-rules` ports the seeder (repo-level connection/rule writes, clear-then-insert).

**Dockerfile:**

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download
COPY apps/api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lazyfga-api ./cmd/lazyfga-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lazyfga-api /lazyfga-api
EXPOSE 8787
ENTRYPOINT ["/lazyfga-api"]
```

(Build context stays the repo root in compose for consistency; only `apps/api` is copied. Parity fixtures are `go:embed`ded, so the image needs no `packages/`.)

**Coverage gate script** (`apps/api/scripts/coverage-gate.sh`, used locally and in CI):

```bash
#!/usr/bin/env bash
set -euo pipefail
go test ./... -race -coverpkg=./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out            # full per-package report in the CI log
total=$(go tool cover -func=coverage.out | awk '/^total:/{sub("%","",$NF); print $NF}')
awk -v t="$total" 'BEGIN{ exit !(t+0 >= 95.0) }' \
  || { echo "FAIL: total coverage ${total}% < 95%"; exit 1; }
echo "OK: total coverage ${total}%"
```

**CI dependencies:** GitHub Actions `services:` cannot set a container command nor sequence OpenFGA's `migrate`-then-`run`, so the go job instead **reuses the repo's `docker-compose.yml`**: a step runs `docker compose up -d --wait postgres openfga` (compose already encodes the init SQL, the `openfga-migrate` one-shot, and healthchecks), then exports `DATABASE_URL`/`OPENFGA_API_URL` for the test run. Locally, dependency-backed tests skip when the env vars are unset; in CI, `LAZYFGA_TEST_INTEGRATION=1` turns those skips into failures so the coverage gate can never silently pass on unit tests alone.

**Docs:** README quickstart becomes: `docker compose up --build` (all-in-one) or `docker compose up -d postgres openfga` + `go run ./cmd/lazyfga-api` for dev; demo `go run ./cmd/demo run`; web unchanged (`pnpm --filter @lazyfga/web dev`); test commands `go test ./...` + `pnpm -r test`. `CLAUDE.md` file-structure tree is updated in the same commit (per repo rule "if change file structure, update this file first").

**Cutover ordering within the final phase:** (1) CLIs land in `apps/api-go` + tests; (2) Dockerfile/compose switched and verified; (3) swap commit (delete TS api, `git mv`, docs, CLAUDE.md, CI) — CI on that commit must be fully green including the coverage gate; (4) E2E verification runs; fixes fold into the same phase before the goal is declared done.

## 5. API Design

### 5-1. New / Modified

No HTTP API changes. New CLI surfaces (env-compatible with the TS scripts):

```
lazyfga-api                    # server (unchanged behavior)
demo run | demo reset          # ports of scripts/demo/{run,reset}.ts
seed-zitadel-rules             # port of scripts/seed-zitadel-rules.ts
```

### 5-2. Error Handling

| Case                                  | Result                                                               |
| ------------------------------------- | -------------------------------------------------------------------- |
| demo preflight: api unreachable       | exit 1 with `api not ready` message (same as TS)                     |
| demo publish/seed HTTP failure        | exit 1 with status + body (409 on policy/connection seeds tolerated) |
| structural tuple write non-idempotent | warn and continue (same as TS)                                       |
| CI coverage below 95%                 | go job fails (hard gate)                                             |
| parity corpus divergence              | parity job fails identifying the case name                           |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                                                                                            | Estimated Duration | Owner        |
| ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------ | ------------ |
| Phase 1 | Go CLIs (demo run/reset, seeder, zitadelsign, healthcheck mode) + tests                                                                                         | 0.3 day            | Seonguk Moon |
| Phase 2 | Dockerfile + compose switch (healthcheck, required ADMIN_TOKEN) + coverage-gate script; local compose-up verification                                           | 0.2 day            | Seonguk Moon |
| Phase 3 | CI rewrite (go/node/parity/license jobs); green run                                                                                                             | 0.2 day            | Seonguk Moon |
| Phase 4 | Dual-backend parity rehearsal (contract replay + volume adoption + demo on both) → swap commit (TS api removal + move + docs + CLAUDE.md) → full E2E acceptance | 0.4 day            | Seonguk Moon |

SSH-signed conventional commits per phase where meaningful (CLIs; infra; CI; swap), Claude+Codex parallel review before the swap commit.

### 6-2. Dependencies

- LFGA-23/24/25/26 merged (full Go backend).
- GitHub Actions: `actions/setup-go`, `golangci/golangci-lint-action`, existing pnpm/bun setup actions for the node lane.
- Local: Docker + compose for E2E; Chrome DevTools MCP for web E2E.

## 7. References

- TS sources being ported/removed: `apps/api/scripts/**`, `apps/api/Dockerfile`, `.github/workflows/ci.yml`.
- Docs to update: `README.md`, `docs/getting-started.md`, `docs/api.md`, `ARCHITECTURE.md`, `CLAUDE.md`.
- [LFGA-22 master plan](lazyfga-22-go-migration-master-plan.md) D9 (cutover), D8 (coverage); `lazyfga-19` (demo scenario, normative).
- distroless images — https://github.com/GoogleContainerTools/distroless
