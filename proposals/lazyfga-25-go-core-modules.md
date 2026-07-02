# Go Core Modules: model, policy, pdp, grants - Spec Proposal

| Item      | Detail                                 |
| --------- | -------------------------------------- |
| Author    | Seonguk Moon                           |
| Created   | 2026-07-02                             |
| Status    | **Implemented**                        |
| Reviewers | Claude (review agent), Codex (gpt-5.5) |

---

## 1. Summary

Port the four core business modules of the TS backend to Go on top of the LFGA-23 foundation and LFGA-24 contracts/compiler: **model** (publish/current/versions/diff), **policy** (named-policy CRUD), **pdp** (AuthZEN evaluation + the reason engine), and **permission** (`/grants` structural grant/revoke/list). Every route keeps its exact method, path, auth role, status codes, and JSON shapes; module behaviors (orphan-model audit trail, deny-by-default reason codes, idempotent write absorption, keyset semantics) are ported rule-for-rule with handler-level tests for every status branch.

Governed by LFGA-22. Depends on LFGA-23 + LFGA-24. Runs in parallel with LFGA-26 (disjoint modules).

## 2. Background & Motivation

- These modules are the product's decision path: publish a model → name a policy → evaluate with reason → manage structural grants. Their behavior is specified by `lazyfga-7/8/9/11/20` and hardened by later reviews; the TS implementation is the normative reference.
- The reason engine (`pdp/reason.ts`) is the most intricate port (bounded graph search with cycle guard and honest truncation flags); its behavior must be preserved exactly because the web explain UI renders its output.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `internal/modules/model`: routes + service + diff + repo with identical semantics (see 4.3).
- [ ] `internal/modules/policy`: routes + service + repo; slug rule, uniqueness race handling via Postgres unique violation (SQLSTATE 23505) → 409.
- [ ] `internal/modules/pdp`: evaluator (single-question template, deny-by-default reason codes, model-version pinning) + reason engine (witnessing path / missing links) + route.
- [ ] `internal/modules/permission`: grant/revoke/list with validation from `contract`, idempotent OpenFGA write absorption, resource/subject list queries with assignable-relation filtering and fan-out reads.
- [ ] All timestamps in responses serialize exactly like JS `Date.toISOString()` (UTC, millisecond precision, `Z` suffix) — a foundation helper `jsontime.Time` is used for every exposed timestamp.
- [ ] Handler tests (httptest + fake Gateway/repos) covering every status branch; repo tests against real Postgres; ≥95% statement coverage on packages introduced here.

### 3.2 Non-Goals

- [ ] No `/tokens`, `/audit`, `/idp` (LFGA-26) — but this proposal _consumes_ `audit.Record` (fire-and-forget) via a narrow interface defined in the foundation so the two proposals stay parallel-implementable (see 4.3 "audit seam").
- [ ] No behavior changes, no new routes, no schema changes.

## 4. Technical Design

### 4.1 Architecture Overview

```
internal/modules/model/       routes.go service.go diff.go repo.go
internal/modules/policy/      routes.go service.go repo.go
internal/modules/pdp/         routes.go evaluator.go reason.go
internal/modules/permission/  routes.go service.go
```

Wiring: each module exposes `Mount(r chi.Router, deps Deps)`; deps are interfaces (Gateway, repos, audit recorder, model repo for cross-module reads) so handlers are unit-testable. Cross-module reads mirror TS: policy/pdp/permission read the current model via the model repo; permission and pdp classify OpenFGA errors via `writeerror`.

**Audit seam:** LFGA-23 owns `type AuditRecorder interface { Record(action string, data map[string]any, actor string) }` **and its final DB-backed write implementation** (insert into `audit_log`, fire-and-forget goroutine, panics/errors swallowed with log) plus the `PrincipalActor` mapping. LFGA-25 and LFGA-26 only consume the interface; LFGA-26 owns the query/read side exclusively. This keeps the parallel implementation conflict-free (the write path has a single owner, LFGA-23, which lands before this wave).

### 4.2 Data Model Changes

None. Reads/writes the existing `model_version`, `instance_config`, `policy` tables.

### 4.3 Core Logic

**Model publish (`POST /model`, admin, port of `model.service.ts`):**

1. Body: `{ir, note?}`; strict decode of `ir` (contract.DecodeModelIR) → 422 `{"error":"invalid IR shape","issues":[...]}`.
2. `ValidateModelIR` → violations ⇒ 422 `{"error":..., "detail":{"validation":[...]}}` (same nesting as TS `PublishError.detail`).
3. `CompileIRToDSL` → `CompileError` ⇒ 422 with `{compile: reason, detail}`.
4. `gateway.WriteAuthorizationModel` → failure ⇒ 502 `{openfga: "..."}`.
5. Single Postgres transaction: insert `model_version` (ir JSONB, dsl, note, createdBy) + update `instance_config.current_model_version_id` and `updated_at`. Failure ⇒ audit `model.publish.db_failure` (with `orphanModelId`) + 500.
6. Success ⇒ audit `model.publish`, respond 201 `{version:{id, authorizationModelId, createdAt}}`.
7. `createdBy` = `"admin"` or `"token:<id>"` from the principal (same rule).

**Model reads:** `GET /model/current` (404 `no model published yet` when unset), `GET /model/versions` (desc by createdAt), `GET /model/diff?from=&to=` (400 when either missing; 404 when either version not found; registered semantics identical), `GET /model/versions/:id` (404). Version ids are UUIDs: the Go repo parses the path param as UUID and treats parse failure as not-found (404) — **approved deviation LFGA-22 §4.4-2** (TS lets Postgres throw on malformed UUIDs → 500; 404 is strictly more correct and web never sends malformed ids). Version responses expose `{id, authorizationModelId, createdAt, note}` plus `ir`/`dsl` for single-version reads (`ir` passed through as raw JSON per LFGA-24).

**Diff:** line-for-line port of `diffModels` — change kinds TYPE_ADDED/REMOVED, ROLE_ADDED/REMOVED, ROLE_ASSIGNABLE_CHANGED, PERMISSION_ADDED/REMOVED, GRANT_CHANGED, PERMISSION_INHERIT_CHANGED, PARENT_ADDED/REMOVED; subject key = `group#member`|`user`; NUL separator for parent pairs; added/removed arrays sorted lexicographically; final deterministic ordering by byte-comparing the JSON encoding of each change (Go replicates the exact JSON key order of the TS object literals to keep the sort permutation identical — field order fixed in the struct definitions and covered by parity tests on corpus IR pairs).

**Policy CRUD:** port of `policy.routes/service/repo`:

- `POST /policies` — 422 when `id/permission/resourceType` missing/non-string; slug `^[a-z0-9-]+$` → 422; duplicate id or (permission,resourceType) → 409 (pre-check + 23505 backstop); target existence in current model (resource type + permission name) → 422 with same messages incl. `can_<permission>` phrasing; 201 `{policy}`; audit `policy.create`.
- `GET /policies` → `{policies:[...]}` desc by createdAt. `GET /policies/:id` → 404 `policy not found`.
- `PUT /policies/:id` — route-level 404 pre-check; merge patch; uniqueness clash → 409; target check → 422; audit `policy.update`.
- `DELETE /policies/:id` — 204 on delete (audited) / 404.
- Policy JSON omits `description`/`conditionRef` when null (TS `undefined` omission).

**PDP evaluate (`POST /access/v1/evaluation`, service|admin):**

- 400 unless `subject.type/id`, `action.name`, `resource.type/id` are non-empty strings (same error message).
- No current model ⇒ 200 `{decision:false, context:{reason_code:"MODEL_NOT_PUBLISHED"}}`; no policy for `(action.name, resource.type)` ⇒ `NO_POLICY`.
- Check `user=<subject.type>:<subject.id>`, `relation=can_<policy.permission>`, `object=<resource.type>:<resource.id>`, context passthrough, pinned to `current.authorizationModelId`. OpenFGA error ⇒ audit `pdp.evaluate.openfga_error` + 500 `{"error":"evaluation failed"}`.
- `options.reason` truthy ⇒ run reason engine with the same pin; reason failure is swallowed (audit `pdp.reason.error`, return decision only).

**Reason engine (port of `reason.ts`):** `MAX_DEPTH=8`; `visited` set keyed `permission|object`; allow path = first role whose Check allows (classify direct vs group-membership via tuple Read + member Check; unclassifiable ⇒ step `direct:false` + `truncated`), else recurse into parent tuples per `inheritFromParents`, exhausting siblings before reporting truncation; deny ⇒ `missingLinks` from the model (roles anyOf + parent needs). Text formats are byte-identical (`"<user> can <perm> <object>: role r (direct) → inherited via rel from folder:x"`, `"denied: needs one of [...] on <object>, or ..."`, `"(partial)"` suffix, fallback `"allowed via can_<p> (path reconstruction incomplete)"`). Deps (`Check`/`Read`) are an interface for fake-driven tests, mirroring `ReasonDeps`.

**Grants (`/grants`, admin, port of `permission.routes/service`):**

- POST: strict decode (grantRequestSchema equivalent) → 400 `{error:"malformed grant request", code:"malformed_request"}`; no published model → 404 `no_published_model`; `ValidateGrant` fail → 400 with its code; `gateway.Write` pinned to published model id; write error → `classifyWriteError`: idempotent ⇒ 200 `{granted:true, created:false}` (not audited), transient ⇒ 502 `openfga_unavailable`, other ⇒ 400 `openfga_invalid_input`; success ⇒ 201 `{granted:true, created:true}` + audit `permission.grant`.
- DELETE: same shape with `revoked/deleted`, always 200 on success-or-noop, audit only on real delete.
- GET: exactly one of `resource` / `subject` query params (else 400); strict ref parsing (400 on invalid); `resourceType` filter validated `^[a-zA-Z0-9_]+$`; resource listing = single Read on object + filter by `IsAssignableRelation`; subject listing = Read per candidate type (`<type>:` prefix) over all model resource+group types (or the single given type), filter, merge; read errors classified (502 transient / 400 deterministic).

## 5. API Design

### 5-1. New / Modified

No new/changed HTTP contracts — the ported surface (all admin unless noted):

```
POST   /model                       201 | 422 | 502 | 500
GET    /model/current               200 | 404
GET    /model/versions              200
GET    /model/diff?from=&to=        200 | 400 | 404
GET    /model/versions/:id          200 | 404
POST   /policies                    201 | 409 | 422
GET    /policies                    200
GET    /policies/:id                200 | 404
PUT    /policies/:id                200 | 404 | 409 | 422
DELETE /policies/:id                204 | 404
POST   /access/v1/evaluation        200 | 400 | 500      (service|admin)
POST   /grants                      201 | 200 | 400 | 404 | 502
DELETE /grants                      200 | 400 | 404 | 502
GET    /grants?resource=|subject=[&resourceType=]  200 | 400 | 404 | 502
```

(401/403 on every route from the shared auth middleware.)

### 5-2. Error Handling

As enumerated per route above; identical bodies to TS (`{"error": ...}` with optional `detail`/`issues`/`code`). Infrastructure failures keep the foundation rules: unexpected errors → 500 via recover/error middleware; auth-layer DB failure → 500 never 401.

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                | Estimated Duration | Owner        |
| ------- | ----------------------------------------------------------------------------------- | ------------------ | ------------ |
| Phase 1 | model: repo + diff + service + routes + tests                                       | 0.3 day            | Seonguk Moon |
| Phase 2 | policy: repo + service + routes + tests                                             | 0.2 day            | Seonguk Moon |
| Phase 3 | pdp: evaluator + reason engine + route + tests (fake deps, every truncation branch) | 0.3 day            | Seonguk Moon |
| Phase 4 | permission: service + routes + tests (idempotency/transient matrices, list fan-out) | 0.2 day            | Seonguk Moon |

One SSH-signed conventional commit at the end, Claude+Codex parallel review before commit.

### 6-2. Dependencies

- LFGA-23 (foundation: chi, middleware, Gateway, writeerror, pgx, AuditRecorder seam, jsontime helper).
- LFGA-24 (contract types/validators, compiler).
- No new third-party Go dependencies.

## 7. References

- TS sources: `apps/api/src/modules/{model,policy,pdp,permission}/**` (normative), tests alongside.
- Specs: `lazyfga-7` (publish), `lazyfga-8` (policy), `lazyfga-9` (AuthZEN evaluate), `lazyfga-11` (reason), `lazyfga-14` (conditions in publish/evaluate), `lazyfga-20` (grants semantics incl. pagination/idempotency hardening).
- [LFGA-22 master plan](lazyfga-22-go-migration-master-plan.md), [LFGA-23](lazyfga-23-go-foundation.md), [LFGA-24](lazyfga-24-go-contracts-compiler.md).
- OpenID AuthZEN 1.0 — https://openid.github.io/authzen/
