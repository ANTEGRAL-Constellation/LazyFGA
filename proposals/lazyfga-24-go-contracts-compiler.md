# Go Contracts & Compiler Port - Spec Proposal

| Item      | Detail       |
| --------- | ------------ |
| Author    | Seonguk Moon |
| Created   | 2026-07-02   |
| Status    | **Approved**  |
| Reviewers | Claude (review agent), Codex (gpt-5.5) |

---

## 1. Summary

Port the backend-consumed surface of `packages/shared` and `packages/compiler` to Go: `internal/contract` (ModelIR types + shape decoding + `validateModelIR`, condition contract + `validateConditionDef`, AuthZEN/policy/reason/audit/grant contracts with the grant validators and tuple-key builders) and `internal/compiler` (`compileIrToDsl` = deterministic DSL emitter + official Go transformer, `conditionToCel`). Establish the **cross-language parity corpus** in `packages/shared/src/__fixtures__/parity/` that both the TS and Go test suites consume, preventing drift between the browser-preview compiler (TS) and the authoritative backend compiler (Go).

Governed by LFGA-22 (D1, D5). Runs in parallel with LFGA-23 (disjoint code: pure logic only, no HTTP/DB/OpenFGA).

## 2. Background & Motivation

- Usage analysis of `apps/api` (src + scripts) shows the backend consumes exactly: from `@lazyfga/compiler` only `compileIrToDsl`/`CompileError`; from `@lazyfga/shared` the types plus these runtime functions: `validateModelIR`, `validateGrant`, `validateRevoke`, `isAssignableRelation`, `grantTupleKey`, `revokeTupleKey`, `subjectToUser`, `parseGrantSubject`, `parseResourceRef`, and the zod shape schemas `modelIrSchema`, `grantRequestSchema`, `revokeRequestSchema`.
- **Not needed in Go for the server** (web-only): `dsl-to-ir`, `coverage`, `tryParseCondition`, `describeCondition`, `policyContextParams`, and `edit.ts`. The web keeps using the TS package for in-browser preview/import. One caveat: the **demo script** (not the server) uses two `edit.ts` helpers (`addCondition`, `setAssignmentCondition`); LFGA-27 reimplements exactly those two operations as demo-local helpers in the Go demo CLI. This shrinks the port and the drift surface: the dual-implemented logic is `ir-to-dsl` + `condition-to-cel` + the validators (+ the two demo helpers).
- The DSL↔AuthModel-JSON layer is officially dual-maintained by OpenFGA (`@openfga/syntax-transformer` for TS, `openfga/language/pkg/go/transformer` for Go, generated from one grammar), so lazyFGA-owned parity reduces to: identical DSL emission, identical CEL emission, identical validation verdicts.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `internal/contract`: Go types marshaling to **byte-compatible JSON** with the TS contracts (field names, optionality, discriminated unions like `SubjectRef.kind`), strict shape decoding equivalent to the zod schemas, `ValidateModelIR` reproducing every rule and error code of `validateModelIR` (BAD_NAME, DUP_TYPE, DUP_RELATION, UNKNOWN_PARENT, UNKNOWN_ROLE, UNKNOWN_GROUP, EMPTY_GRANT, RESERVED_USER, PARENT_MISSING_PERMISSION, DUP_PARENT_RELATION, EMPTY_SUBJECTS, CONDITION_UNKNOWN, DUP_CONDITION + promoted condition codes) with identical `path` strings.
- [ ] Condition contract: `ValidateConditionDef` (BAD_NAME, DUP_PARAM, UNKNOWN_PARAM, TYPE_MISMATCH, BAD_CIDR, BAD_TIMESTAMP, EMPTY_GROUP) with the same RFC3339 regex, conservative CIDR check, CEL reserved-word set, and bool-ordering-op rejection.
- [ ] Grant contract: `ValidateGrant`/`ValidateRevoke`/`IsAssignableRelation` (same decision table incl. condition-required/not-permitted rules), `SubjectToUser`, `GrantTupleKey`/`RevokeTupleKey`, strict `ParseResourceRef`/`ParseGrantSubject` (forbidden chars `: # * whitespace` in ids).
- [ ] `internal/compiler`: `CompileIRToDSL` — defensive re-validation, deterministic emitter (header → groups in input order → resources in input order with parents→roles→permissions → top-level condition blocks), official Go transformer for DSL→AuthModel JSON, `CompileError` with reasons `IR_INVALID` / `JSON_TRANSFORM_FAILED`; `ConditionToCel` — same operator tables, `timestamp("...")`/param RHS, `.in_cidr("...")`, JSON-string literal quoting, flat top-level/parenthesized nested groups, single-child unwrapping, empty-group → `true` defense.
- [ ] Parity corpus at `packages/shared/src/__fixtures__/parity/`: named cases of `(ir, expected dsl)`, `(ir, expected validation error codes+paths)`, `(condition def, expected cel | expected error codes)`, `(grant request + model, expected verdict)`, plus JSON marshal/unmarshal round-trip cases. Both TS (bun test) and Go (`go test`) run the same files; CI runs both (LFGA-27 wires the job).
- [ ] cel-go compile/typecheck test for generated CEL of the pure subset; `ipaddress`/`in_cidr` covered by corpus strings + E2E (LFGA-22 D5).
- [ ] ≥95% statement coverage for the packages introduced here (pure logic — target effectively ~100%).

### 3.2 Non-Goals

- [ ] No HTTP handlers, DB, or OpenFGA gateway (LFGA-23/25/26).
- [ ] No port of web-only logic (`dsl-to-ir`, `coverage`, `edit`, `describeCondition`, `policyContextParams`, `tryParseCondition`). If a future feature needs DSL import server-side, that is a new proposal.
- [ ] No contract changes: field names, error codes, and DSL output stay exactly as the TS implementation produces today.
- [ ] TS packages are not modified except for **adding** the parity corpus and its TS-side test file.

## 4. Technical Design

### 4.1 Architecture Overview

```
internal/contract/
  ident.go        # IDENT_RE, RESERVED_WORDS, CEL_RESERVED (ported constants)
  model.go        # SubjectRef, GroupType, ParentRef, Role, Permission, ResourceType, ModelIR
  model_decode.go # strict JSON shape decoding (zod-equivalent) → *ModelIR or field errors
  model_validate.go # ValidateModelIR (rules 1–8 + condition refs)
  condition.go    # ConditionParam(Type), TimeRhs, ConditionLeaf/Group/Node/Def (+custom (un)marshal)
  condition_validate.go # ValidateConditionDef
  authzen.go      # EvaluationRequest/Response
  policy.go       # Policy
  reason.go       # ReasonResult, ReasonStep, MissingLink
  audit.go        # AuditEntry
  grant.go        # Grant types, validators, tuple keys, parsers
internal/compiler/
  ir_to_dsl.go    # CompileIRToDSL (+ emitDsl), CompileError
  condition_to_cel.go # ConditionToCel
packages/shared/src/__fixtures__/parity/   # corpus consumed by BOTH languages
  model-cases.json · condition-cases.json · grant-cases.json · dsl-cases.json
packages/shared/src/parity.test.ts         # TS side runs the corpus
internal/.../parity_test.go                # Go side runs the corpus, located via internal/testutil.RepoPath()
                                           # (runtime.Caller-anchored repo-root resolver — depth-independent,
                                           #  works before and after the apps/api-go → apps/api move)
```

### 4.2 Data Model Changes

None (pure logic; no DB).

### 4.3 Core Logic

**JSON compatibility rules (applies to every contract type):**

- Field names match the TS interfaces exactly (`schemaVersion`, `memberTypes`, `assignableBy`, `grantedByRoles`, `inheritFromParents`, `occurredAt`, …).
- Optional fields are omitted when absent; fields whose zero value is meaningful are **never** `omitempty` — explicitly: `ReasonStep.direct` (bool, web reads `false`) and `ReasonStep.on` always serialize; `ReasonResult.truncated`/`path`/`missingLinks`, `SubjectRef.condition`, `Policy.description`/`conditionRef`, `GrantEntry.condition`, `ReasonStep.group`/`groupObject`/`parentObject` omit when absent. Custom marshalers where `omitempty` can't express this. Discriminated unions (`SubjectRef`, `ConditionLeaf`/`ConditionNode`, `TimeRhs`) get custom `MarshalJSON`/`UnmarshalJSON` keyed on `kind` (or group-vs-leaf presence of `children`), rejecting unknown discriminators exactly like zod. Per-type marshal/unmarshal round-trip cases live in the parity corpus.
- Strict decoding mirrors zod semantics used today: wrong type / missing required field / unknown discriminator ⇒ decode error carrying a path + message list (consumed by LFGA-25 for the 422 `issues` response). The `issues` array is `[{path, message}]` in Go — this is **approved deviation LFGA-22 §4.4-1** (same top-level response shape and status; zod internals are not a public contract; web only displays them, verified in LFGA-27 E2E).
- Stored JSON passthrough: values persisted as JSONB and returned verbatim (`model_version.ir_json`, `audit_log.data`, `idp_mapping_rule.match`/`tuple_template`) are carried as raw JSON (`json.RawMessage`) wherever the handler does not need typed access, so Go never re-formats numbers/strings that TS/Postgres wrote. Typed access (e.g. grants validation against the IR) decodes a copy without ever re-serializing it into responses.

**ValidateModelIR:** direct port. Error **codes and `path` strings must match byte-for-byte** (e.g. `resources[1].permissions[0].grantedByRoles[2]`), since web renders them and the parity corpus asserts them. Same collection semantics: collect-all (never throw), first-occupier-wins for the relation namespace, `DUP_PARENT_RELATION` for both duplicate relationName and duplicate parentTypes entries, rule 7 checked per inherit entry against every parentType.

**ValidateConditionDef:** same regexes (`RFC3339_RE`, decimal prefix rules in CIDR), same `Number.isSafeInteger` analogue for `int` (value is a float64 from JSON: require integral and |v| ≤ 2^53−1), `double` requires finite, bool ordering ops rejected.

**Grant validators:** port the decision table exactly, including: role lookup before group lookup; group only assignable via `member`; forbidden-char checks before assignability; condition rules (named condition must exist in model **and** be attached to a matching SubjectRef; conditionless grant requires at least one conditionless matching ref).

**Compiler determinism:** `emitDsl` is a line-for-line port; output must be byte-identical to TS for every corpus IR (assertion in both suites against the same expected-DSL strings). DSL→JSON delegates to `transformer.TransformDSLToJSON` (official Go package); the JSON string is unmarshaled into the SDK's `WriteAuthorizationModelRequest`-compatible struct by LFGA-25 at the publish boundary. Failure of the transform maps to `CompileError{Reason: JSON_TRANSFORM_FAILED}`.

**ConditionToCel:** same emission, guaranteed by two dedicated JS-compat helpers with exhaustive corpus cases:

- `jsJSONString(s)` — reproduces `JSON.stringify` for strings exactly. Notably Go's `encoding/json` HTML-escapes `< > &` and JS does not, so the helper uses a non-HTML-escaping encoder; corpus cases cover `< > &`, U+2028/U+2029 (JS emits them raw), control characters (`\u00XX` forms), quotes, backslashes, and multi-byte unicode.
- `jsNumberString(f)` — reproduces ECMA-262 `Number::toString` (shortest round-trip decimal, decimal notation for magnitudes in `[1e-6, 1e21)`, exponent form outside with JS exponent formatting — Go's `strconv` pads exponents like `1e-07` where JS prints `1e-7`, so the helper normalizes), including `-0 → "0"`, integer-valued floats without a decimal point, and safe-integer boundaries. Corpus cases: `-0`, `1e-7`, `1e-6`, `1e20`, `1e21`, `2^53±1`, `0.1+0.2`-style shortest-repr values.

These helpers are also the single source for any number/string the idp extraction engine coerces (LFGA-26 imports them), keeping cross-module formatting parity in one place.

**Parity corpus:** file format per case: `{ "name": "...", "ir": {...}, "dsl": "...", "validationErrors": [{code, path}]?, ... }`. Corpus contents: every DSL construct the emitter supports (groups incl. conditioned member types, multi-parent, multi-role/permission unions, inherit-only-plus-role, conditions with every param type and every leaf/op, nested groups, single-child groups), every validation error code at least once, grant decision-table rows, and edge strings (unicode/escapes in condition literals). TS test asserts current behavior == corpus (guarding the reference implementation); Go test asserts Go == corpus (guarding the port); transitively TS == Go.

## 5. API Design

### 5-1. New / Modified

No HTTP APIs. Key Go signatures:

```go
// DecodeModelIR strictly decodes untrusted JSON into ModelIR (zod-equivalent
// shape validation). Returns field-level issues on shape violations.
func contract.DecodeModelIR(data []byte) (*ModelIR, []Issue)

// ValidateModelIR statically validates an IR (rules 1–8 + condition rules),
// collecting all violations; empty slice means valid. Never panics.
func contract.ValidateModelIR(ir *ModelIR) []ValidationError

// ValidateConditionDef statically validates a condition definition.
func contract.ValidateConditionDef(def *ConditionDef) []ConditionError

// ValidateGrant / ValidateRevoke check structural assignability and
// (for grant) the condition rules against the published model IR.
func contract.ValidateGrant(model *ModelIR, req *GrantRequest) GrantValidation
func contract.ValidateRevoke(model *ModelIR, req *RevokeRequest) GrantValidation

func contract.SubjectToUser(s GrantSubject) string
func contract.GrantTupleKey(req *GrantRequest) GrantTupleKey
func contract.RevokeTupleKey(req *RevokeRequest) TupleRef
func contract.ParseResourceRef(s string) (ResourceRef, bool)
func contract.ParseGrantSubject(s string) (GrantSubject, bool)
func contract.IsAssignableRelation(model *ModelIR, typ, relation string) bool

// CompileIRToDSL compiles a ModelIR into the deterministic .fga DSL string and
// the AuthorizationModel JSON (via the official openfga/language transformer).
func compiler.CompileIRToDSL(ir *contract.ModelIR) (dsl string, modelJSON []byte, err error) // err: *CompileError

// ConditionToCel renders a condition definition into its OpenFGA declaration
// header and CEL body (deterministic).
func compiler.ConditionToCel(def *contract.ConditionDef) (decl string, cel string)
```

### 5-2. Error Handling

No HTTP status codes (library layer). Error taxonomy:

| Error                         | Meaning / consumer                                                        |
| ----------------------------- | ------------------------------------------------------------------------- |
| `[]Issue` (decode)            | shape violation; LFGA-25 maps to 422 `{"error":"invalid IR shape",issues}` |
| `[]ValidationError`           | semantic IR violation; LFGA-25 maps to 422 `{validation: [...]}`           |
| `[]ConditionError`            | promoted into ValidationError by ValidateModelIR (same as TS)               |
| `GrantValidation{ok:false}`   | LFGA-25 maps code→400 with `{error, code}`                                  |
| `CompileError(IR_INVALID)`    | defensive re-validation failed; LFGA-25 maps to 422                         |
| `CompileError(JSON_TRANSFORM_FAILED)` | official transformer rejected emitted DSL; LFGA-25 maps to 422      |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                              | Estimated Duration | Owner        |
| ------- | -------------------------------------------------------------------------------------------------- | ------------------ | ------------ |
| Phase 1 | `internal/contract`: types + custom (un)marshalers + strict decode + ident constants + tests        | 0.3 day            | Seonguk Moon |
| Phase 2 | Validators: ValidateModelIR, ValidateConditionDef, grant validators/parsers/tuple keys + tests      | 0.3 day            | Seonguk Moon |
| Phase 3 | `internal/compiler`: emitter + transformer integration + ConditionToCel + cel-go check + tests      | 0.2 day            | Seonguk Moon |
| Phase 4 | Parity corpus authoring + TS parity test + Go parity test (both green)                              | 0.2 day            | Seonguk Moon |

One SSH-signed conventional commit at the end, Claude+Codex parallel review before commit.

### 6-2. Dependencies

- `github.com/openfga/language` (Go transformer), `github.com/google/cel-go` (test-only), `github.com/stretchr/testify` (test-only).
- TS side: no new runtime deps (corpus test uses existing bun test setup in `packages/shared`/`packages/compiler`).
- LFGA-22 decisions D1/D5; independent of LFGA-23.

## 7. References

- TS sources being ported: `packages/shared/src/{ident,model,condition,authzen,policy,reason,audit,grant}.ts`, `packages/compiler/src/{ir-to-dsl,condition-to-cel}.ts` and their tests.
- Web-only (explicitly not ported): `packages/compiler/src/{dsl-to-ir,coverage}.ts`, `packages/shared/src/edit.ts`.
- [LFGA-22 master plan](lazyfga-22-go-migration-master-plan.md); original specs `lazyfga-2/3/13/14/20`.
- openfga/language Go transformer — https://github.com/openfga/language/tree/main/pkg/go
- cel-go — https://github.com/google/cel-go
