# Go Platform Modules: tokens, audit, idp - Spec Proposal

| Item      | Detail       |
| --------- | ------------ |
| Author    | Seonguk Moon |
| Created   | 2026-07-02   |
| Status    | **Approved**  |
| Reviewers | Claude (review agent), Codex (gpt-5.5) |

---

## 1. Summary

Port the platform modules to Go: **auth tokens** (`/tokens` issue/list/revoke), **audit** (read API with keyset pagination; the write side lives in the LFGA-23 seam), and the **idp webhook framework** (`/idp`): configurable signature verification engine, declarative event extraction, in-repo provider presets (`zitadel`, `standard-webhooks`), mapping engine with array fan-out, webhook route, and connection/rule CRUD. All semantics — including the security hardenings (strict decimal timestamps, constant-time comparisons, no-DB-audit for unauthenticated requests, injection guards in tuple templates) — are ported rule-for-rule.

Governed by LFGA-22. Depends on LFGA-23 + LFGA-24. Runs in parallel with LFGA-25 (disjoint modules).

## 2. Background & Motivation

- These modules carry the operational/security surface: PDP caller control (`lazyfga-10`), change auditability (`lazyfga-17`), and IdP identity sync (`lazyfga-15/16/21`). The idp signature/extraction/mapping engines encode several review-hardened invariants (replay window, raw-timestamp signing, no-amplification auditing, template injection guards) that must survive the port intact.
- ZITADEL signing semantics were verified against ZITADEL source (`pkg/actions/signing.go`, seconds timestamps, multiple `v1` signatures); the Go port must keep byte-level signing-payload behavior.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `internal/modules/auth`: token routes + repo (issue with one-time plaintext, list without hashes, revoke idempotence rules) — the middleware/`generateToken` primitives come from LFGA-23.
- [ ] `internal/modules/audit`: query repo (keyset pagination, filters, cursor codec) + `GET /audit` route. The write-side `AuditRecorder` (interface **and** DB-backed implementation) is owned by LFGA-23 and only consumed here — this proposal owns the read side exclusively (parallel-wave conflict avoidance).
- [ ] `internal/modules/idp`: `signature.go` (WebhookSignatureSpec engine: `kv_t_v` / `scheme_hex` / `standard_webhooks` header formats, timestamp sources/units, payload templates `{body} {timestamp} {id}`, hex/base64 encodings, secret decoding with optional prefix, ± tolerance window, multi-signature, constant-time compare), `extraction.go` (dotted-path engine, event-type read, per-event-type rules, scalar coercion, array attributes), `presets.go` (zitadel + standard-webhooks, field-for-field identical specs), `mapping.go` (match predicates, `{{...}}` template rendering with injection guards, fan-out, priority-stable application, applied/skipped/failed counters), `repo.go` (connections + rules), `routes.go` (webhook + CRUD).
- [ ] Webhook security invariants preserved: 256 KiB body limit before buffering (413), unauthenticated failures never write DB audit (401 + app log only), unknown provider 404, disabled connection 403, unknown preset 500, invalid JSON 400, transient OpenFGA write ⇒ 502 (IdP retry), signing secret never exposed by any read API.
- [ ] ≥95% statement coverage on packages introduced here (signature/extraction/mapping are pure and table-testable; routes via httptest).

### 3.2 Non-Goals

- [ ] No new presets, no per-connection extraction overrides, no event-batch payloads (single-event semantics as today).
- [ ] No changes to webhook response contract (`{applied, skipped, failed}`) or rule storage format (`idp_mapping_rule` rows).
- [ ] Core modules (LFGA-25) and cutover/demo CLIs (LFGA-27) are out of scope.

## 4. Technical Design

### 4.1 Architecture Overview

```
internal/modules/auth/    routes.go repo.go       # service_token issue/list/revoke
internal/modules/audit/   recorder.go repo.go routes.go cursor.go
internal/modules/idp/     types.go signature.go extraction.go presets.go
                          mapping.go repo.go routes.go
```

All engines take dependencies as values/interfaces (headers as `http.Header`, clock as `func() time.Time` for signature tolerance tests, `WriteTuple` callback for mapping application) — mirroring the TS injection points (`ReasonDeps`-style) so pure tests need no HTTP/DB.

### 4.2 Data Model Changes

None. Uses existing `service_token`, `audit_log`, `idp_connection`, `idp_mapping_rule` tables.

### 4.3 Core Logic

**Tokens:** `POST /tokens` — trimmed non-empty `name` required (400); generate 32-byte base64url plaintext + sha256 hex (LFGA-23 helper); insert; 201 `{id, name, token}` (only exposure of plaintext); audit `token.create`. `GET /tokens` — `{tokens:[{id,name,createdAt,lastUsedAt,revoked}]}` (revoked = revokedAt≠null; hash never serialized). `DELETE /tokens/:id` — revoke only if currently active (`revoked_at IS NULL`), 204 + audit, else 404. Malformed UUID path params → 404 here and on `/idp/connections/:id`, `/idp/connections/:id/rules`, `/idp/rules/:ruleId` (**approved deviation LFGA-22 §4.4-2**).

**Audit recorder (write):** owned by LFGA-23 (interface + DB-backed implementation + `PrincipalActor` mapping: admin → `"admin"`, service → `"service:<tokenId>"`). This module passes `"idp:<provider>"` as the actor on webhook paths and otherwise only consumes `Record`.

**Audit query (`GET /audit`, admin):**

- `limit`: `trunc(Number(limit)) || 50` clamped to [1,200] — Go: parse float, truncate, non-numeric/0 → 50, clamp (identical results for identical inputs; covered by table tests incl. `1.5`, `"abc"`, `0`, `1000`).
- `cursor`: base64url(no padding) of `<ISO occurredAt>|<uuid>`; invalid → 400 `invalid cursor`.
- `from`/`to`: Go accepts RFC3339 (with or without fractional seconds) and `YYYY-MM-DD` (UTC midnight); anything else → 400 `invalid from/to (use ISO 8601)` (**approved deviation LFGA-22 §4.4-3** — narrower than JS `new Date`, documented in docs/api.md).
- `action` filter: exact match, or trailing `*` ⇒ prefix LIKE with `\ % _` escaped; `actor` exact.
- Keyset: `WHERE ... AND (occurred_at < $c OR (occurred_at = $c AND id < $id)) ORDER BY occurred_at DESC, id DESC LIMIT limit+1`; `nextCursor` from the last returned row when more exist. ms-precision timestamps round-trip exactly (column is precision-3; `jsontime` formats ms).

**Signature engine:** port of `signature.ts` with these invariants:

- Timestamps are handled as **raw strings** end-to-end for payload assembly (never numerically normalized); strict `^[0-9]+$` acceptance; tolerance check parses to int and compares `|now−ts| ≤ toleranceSec` (with `millis` unit divided first).
- `kv_t_v`: split on `,`, collect `t`/`v1` (multi-v1 kept in order); `scheme_hex`: scheme label must equal the algorithm (case-insensitive); `standard_webhooks`: whitespace-separated `v1,<b64>` tokens.
- `allowMultipleSignatures=false` keeps only the first signature.
- Payload template substitution: `{timestamp}` (required if present), `{id}` (from `idSource` header; missing/empty ⇒ false), `{body}` (raw bytes as string).
- HMAC-SHA256 → hex/base64 encoded compare via decode-then-`subtle.ConstantTimeCompare` with non-empty equal-length requirement; any decode failure ⇒ false. Secret key bytes: optional prefix strip + raw/base64 decode.
- Clock injected for tests; production uses `time.Now`.

**Extraction engine:** dotted-path getter over the `encoding/json` value tree (Go has no prototype chain, so the TS `__proto__`/own-property guards have no Go equivalent; the dangerous-key rejection is kept for spec parity so the same payloads produce the same results). Path semantics match TS `getPath` exactly, including that **arrays are traversable by numeric segments** (TS `hasOwnProperty` on a numeric index; Go: index into `[]any` when the segment parses as a valid in-range index) — covered by ported test cases. `readEventType` (string, non-empty), first matching rule by event type, subject id must be non-empty **string** (no coercion), attributes: scalars coerced via JS-`String` semantics (string/number/bool; numbers formatted like JS — reuse the LFGA-24 `jsNumberString` helper), arrays → filtered scalar strings (empty arrays preserved), others omitted.

**Presets:** field-for-field port of `PRESETS` (`zitadel`: header `ZITADEL-Signature`, kv_t_v, seconds in-header timestamp, `{timestamp}.{body}`, hex, raw secret, 300 s tolerance, multi-sig; extraction rules for `user.human.added`/`selfregistered` (subject = `aggregateID`, org = `resourceOwner`), `user.grant.added` (subject = `event_payload.userId`, project + roleKeys), `user.grant.removed` (project only); `standard-webhooks`: `webhook-signature` v1-list, separate `webhook-timestamp`, id `webhook-id`, `{id}.{timestamp}.{body}`, base64, `whsec_` base64 secret; extraction `user.created/updated`).

**Mapping engine:** port of `mapping.ts` — regexes `TYPE_ID_RE`/`RELATION_RE`/`FORBIDDEN_IN_VALUE`/`LITERAL_TYPE_PREFIX`; scalar placeholder resolution (`item`/`type`/`subject`/`subject.id`/`subject.type`/`attributes.<k>`; arrays in scalar slots ⇒ error); `matchRule` (eventType equality, all predicates equal on scalar values, fan-out requires non-empty array); `renderTuple` (literal type-prefix requirement on user/object templates, per-placeholder forbidden-char check, post-render shape regexes); `applyEvents` (priority-ascending **stable** sort — `sort.SliceStable` to match JS stable sort; per-item render failures count `failed` and continue; idempotent writes count `skipped`; transient write errors abort with 502; audit actions `idp.tuple.write|delete|skip|error` with same payload keys).

**Webhook route flow:** identical order to TS (where the body-limit **middleware** wraps the handler, so the size check fires first): body limit 256 KiB (413, before any lookup — LFGA-23 middleware) → connection lookup (404) → enabled (403) → preset resolve with `preset ?? provider` fallback (500 + app log on unknown) → read raw body → verify signature (401, app log only) → JSON parse (400) → extract (null ⇒ audit `idp.webhook.no_events` with observed eventType + 200 `{applied:0,skipped:0,failed:0}`) → load provider rules → apply with per-tuple gateway writes (classification identical) → 200 result or 502 `upstream unavailable`.

**Connections/rules CRUD:** port of the route validations: connection create 422 on empty provider/secret, unknown preset 422 listing known keys, duplicate provider 409 (unique violation detection via SQLSTATE 23505 instead of TS string sniffing — same outcomes); update validates patch fields incl. non-empty secret; secret never returned (`PublicConnection`); delete cascades rules (FK). Rule create/update validations: eventType string, op `write|delete`, tupleTemplate three strings, `match[]` field/equals strings, integer priority, and the **fanOut cross-validation** (non-empty string; template must reference `{{item}}` iff fanOut set; fanOut must be an attribute the preset produces for the merged eventType when the preset is known — merged-view validation on partial updates preserved). Rules list per connection ordered by priority asc; rules resolved for webhooks via provider join ordered by priority asc.

## 5. API Design

### 5-1. New / Modified

No new/changed HTTP contracts — ported surface:

```
POST   /tokens                          201 | 400        (admin)
GET    /tokens                          200              (admin)
DELETE /tokens/:id                      204 | 404        (admin)
GET    /audit?[action][actor][from][to][limit][cursor]  200 | 400  (admin)
POST   /idp/webhook/:provider           200 | 400 | 401 | 403 | 404 | 413 | 500 | 502   (signature auth)
POST   /idp/connections                 201 | 409 | 422  (admin)
GET    /idp/connections                 200              (admin)
PUT    /idp/connections/:id             200 | 404 | 422  (admin)
DELETE /idp/connections/:id             204 | 404        (admin)
GET    /idp/connections/:id/rules       200 | 404        (admin)
POST   /idp/connections/:id/rules       201 | 404 | 422  (admin)
PUT    /idp/rules/:ruleId               200 | 404 | 422  (admin)
DELETE /idp/rules/:ruleId               204 | 404        (admin)
```

Key internal signatures:

```go
// VerifyWebhookSignature verifies a raw webhook body against a declarative
// signature spec. Returns false for any parse/window/compare failure.
func idp.VerifyWebhookSignature(spec WebhookSignatureSpec, rawBody []byte, h http.Header, secret string, now func() time.Time) bool

// ExtractEvent normalizes a parsed payload into the canonical IdpEvent using
// the preset's per-event-type extraction rules; nil when the event is not mapped.
func idp.ExtractEvent(preset ProviderPreset, body any) *IdpEvent

// ApplyEvents matches rules (priority-stable), renders tuples with injection
// guards (fan-out aware), and applies them via deps; transient write errors abort.
func idp.ApplyEvents(events []IdpEvent, rules []MappingRule, deps ApplyDeps) (ApplyResult, error)
```

### 5-2. Error Handling

As enumerated per route above, byte-compatible bodies (`{"error": ...}`, webhook result `{applied,skipped,failed}`). Security-sensitive rules restated: 401 signature failures produce **no DB audit row**; secrets never serialize; unauthenticated body buffering capped at 256 KiB.

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                       | Estimated Duration | Owner        |
| ------- | -------------------------------------------------------------------------------------------- | ------------------ | ------------ |
| Phase 1 | tokens routes/repo + audit recorder/repo/route/cursor + tests                                 | 0.3 day            | Seonguk Moon |
| Phase 2 | idp signature + extraction + presets engines + exhaustive table tests (ported TS test cases)  | 0.3 day            | Seonguk Moon |
| Phase 3 | idp mapping engine + repo + tests                                                             | 0.2 day            | Seonguk Moon |
| Phase 4 | idp routes (webhook + CRUD) + httptest matrices                                               | 0.2 day            | Seonguk Moon |

One SSH-signed conventional commit at the end, Claude+Codex parallel review before commit.

### 6-2. Dependencies

- LFGA-23 (middleware/auth primitives, Gateway, writeerror, pgx, AuditRecorder — interface **and** DB write implementation — plus PrincipalActor, jsontime, body-limit middleware) and LFGA-24 (jsJSONString/jsNumberString helpers; no contract types beyond AuditEntry).
- No new third-party Go dependencies (crypto/hmac, crypto/sha256, crypto/subtle, encoding/base64 from stdlib).

## 7. References

- TS sources: `apps/api/src/modules/{auth,audit,idp}/**` and tests (`signature.test.ts`, `extraction.test.ts`, `mapping.test.ts`, `presets.test.ts`, `idp.classify.test.ts` — their cases are ported into the Go tables).
- Specs: `lazyfga-10` (tokens), `lazyfga-17` (audit), `lazyfga-15/16/21` (webhook core, ZITADEL semantics, configurable framework).
- ZITADEL Actions signing (`pkg/actions/signing.go`) and Standard Webhooks spec — https://www.standardwebhooks.com/
- [LFGA-22 master plan](lazyfga-22-go-migration-master-plan.md), [LFGA-23](lazyfga-23-go-foundation.md).
