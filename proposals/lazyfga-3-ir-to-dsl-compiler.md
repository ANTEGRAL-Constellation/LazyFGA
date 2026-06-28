# IR → OpenFGA DSL Compiler - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M1 cross-review)  |

---

## 1. Summary

`ModelIR`(`lazyfga-2`)를 OpenFGA의 `.fga` DSL과 `AuthorizationModel` JSON으로 변환하는 순방향 컴파일러를 정의한다. 이 변환은 결정적(deterministic)이며 동일 IR → 동일 출력이어야 한다. 제품의 "노드로 그린 모델이 실제 OpenFGA 모델이 된다"를 실현하는 핵심.

## 2. Background & Motivation

- 비주얼 저작의 가치는 "그린 것이 곧 OpenFGA 모델"일 때 성립한다. 변환이 신뢰 가능(결정적·검증됨)해야 사용자가 DSL을 안 봐도 된다.
- web(실시간 미리보기)과 api(발행 시 권위 검증)가 **같은 함수**를 호출해야 drift가 없다(ARCHITECTURE 결정 1).

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `compileIrToDsl(ir): { dsl: string; model: AuthModelJSON }` 구현.
- [ ] 5 primitive → DSL 매핑 규칙을 완전·결정적으로 정의(§4.3).
- [ ] `lazyfga-2` 레퍼런스 픽스처에 대한 골든 출력 테스트(DSL 문자열 + JSON).
- [ ] 변환 전 `validateModelIR` 통과를 전제로 하고, 위반 시 컴파일하지 않는다.

### 3.2 Non-Goals

- [ ] 역방향(DSL→IR)·coverage(= `lazyfga-4`).
- [ ] 조건/CEL emit(= `lazyfga-14`). 본 명세는 `Permission.condition`이 항상 undefined라고 가정.

## 4. Technical Design

### 4.1 Architecture Overview

```
ModelIR ──validateModelIR──▶ (ok) ──compileIrToDsl──▶ { dsl, model }
                                   │
                                   └── @openfga/frontend-utils (DSL ⇄ JSON transformer)
```
DSL 문자열 생성은 자체 직렬화로 수행하고, `AuthorizationModel` JSON은 `@openfga/frontend-utils`의 DSL→JSON 변환기로 얻는다(공식 변환기를 신뢰하여 JSON 정합성 확보).

### 4.2 Data Model Changes

변경 없음(순수 변환).

### 4.3 Core Logic

결정적 emit 순서와 규칙(어떤 프로그래머가 보아도 동일 결과):

1. **헤더:** `model\n  schema 1.1\n` + `type user`.
2. **그룹**(IR.groups, **IR 배열 입력 순서 유지**):
   `type <g.name>\n  relations\n    define member: [<subjects(g.memberTypes)>]`
3. **리소스**(IR.resources, **IR 배열 입력 순서 유지**). 각 ResourceType에서 relation emit 순서 = ① parents ② roles ③ permissions, 각 그룹 내 **배열 입력 순서 유지**:
   - **parent**: 각 `p`에 대해 `define <p.relationName>: [<p.parentTypes를 ", "로 연결>]`
   - **role**: 각 `r`에 대해 `define <r.name>: [<subjects(r.assignableBy)>]`
   - **permission**: 각 `perm`에 대해
     `define can_<perm.name>: <UNION>`
     여기서 `UNION` = `perm.grantedByRoles`(입력 순서 유지) 를 ` or `로 연결 + `perm.inheritFromParents`의 각 `rel`에 대해 ` or can_<perm.name> from <rel>` 추가.
4. **subjects(refs) 직렬화:** 각 `SubjectRef` → `user` 또는 `<group>#member`. 출력 순서 = IR 배열 순서. 결과를 `[...]`로 감싼다(쉼표+공백 구분).

매핑 예(IR 픽스처 → DSL 일부):
```
// permission read: grantedByRoles=[viewer,editor,owner], inheritFromParents=[parent]
define can_read: viewer or editor or owner or can_read from parent
```

전제/불변식:
- 호출자는 `compileIrToDsl` 전에 `validateModelIR(ir)`가 빈 배열임을 보장한다. 컴파일러는 재검증을 호출하고, 위반 발견 시 `CompileError`를 던진다(방어).
- 결정성: IR 배열 순서를 그대로 따르므로 동일 IR은 바이트 단위로 동일한 DSL을 낸다. IR이 정규형(canonical)이며, `lazyfga-4` 역변환은 DSL 선언 순서를 IR 배열 순서로 보존해 왕복 안정성을 유지한다(diff·골든 테스트 안정).

### 4.4 Worked Example (요약)

`doc-folder-team.ir.json` → 기대 DSL:
```
model
  schema 1.1
type user
type team
  relations
    define member: [user, team#member]
type folder
  relations
    define owner: [user, team#member]
    define editor: [user, team#member]
    define viewer: [user, team#member]
    define can_read: viewer or editor or owner
type document
  relations
    define parent: [folder]
    define owner: [user, team#member]
    define editor: [user, team#member]
    define viewer: [user, team#member]
    define can_read: viewer or editor or owner or can_read from parent
```
(이 출력이 골든 픽스처가 된다.)

## 5. API Design

### 5-1. New / Modified

```ts
// packages/compiler/src/ir-to-dsl.ts
/**
 * ModelIR을 OpenFGA DSL 문자열과 AuthorizationModel JSON으로 컴파일한다.
 * 결정적: 동일 IR → 바이트 단위 동일 DSL. 호출 전 validateModelIR 통과 전제.
 * @throws CompileError  IR 검증 위반 또는 JSON 변환 실패 시
 */
export function compileIrToDsl(ir: ModelIR): { dsl: string; model: AuthModelJSON };

export class CompileError extends Error {
  constructor(public reason: "IR_INVALID" | "JSON_TRANSFORM_FAILED", public detail: unknown) { super(); }
}
```

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| IR 검증 위반(방어 재검증 실패) | `CompileError("IR_INVALID", ValidationError[])` |
| DSL→JSON 변환기 실패 | `CompileError("JSON_TRANSFORM_FAILED", cause)` |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                      | Estimated | Owner |
|---------|-----------------------------------------------------------|-----------|-------|
| Phase 1 | subjects/relation emit 규칙 + DSL 직렬화                  | 1d        | TBD   |
| Phase 2 | `@openfga/frontend-utils`로 DSL→JSON + 정합 검증          | 0.5d      | TBD   |
| Phase 3 | 골든 테스트(픽스처→DSL/JSON) + 결정성(반복 동일) 테스트    | 0.5d      | TBD   |

### 6-2. Dependencies

- `@openfga/frontend-utils` (DSL ⇄ AuthorizationModel JSON transformer).
- `packages/shared`(`ModelIR`, `validateModelIR`).

## 7. References

- [openfga/frontend-utils](https://github.com/openfga/frontend-utils) — 모델 저작 프론트용 변환 유틸
- [OpenFGA Configuration Language](https://openfga.dev/docs/configuration-language)
- `lazyfga-2-model-ir` (IR 정의)
