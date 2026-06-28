# DSL → IR (역변환) + Coverage 경계 - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M1 cross-review)  |

---

## 1. Summary

OpenFGA `.fga` DSL을 파싱해 `ModelIR`로 되돌리는 역변환과, "비주얼이 표현 가능한 subset"의 경계를 판정하는 `coverage`를 정의한다. 이것이 CONCEPT의 **"양방향 sync는 지원 범위 안에서만, 그 밖은 read-only"** 와 컨셉 모순 #2/#3 해결을 단일 지점에서 구현한다.

## 2. Background & Motivation

- 컨셉 검토에서 확인된 모순(`lazyfga concept gaps` #2/#3): "전체 DSL escape hatch"와 "양방향 sync"는 무제한 양립 불가. 표현 못 하는 고급 구문은 round-trip이 깨진다.
- 해결책은 **경계를 명시적 코드로**: subset 안이면 IR로 왕복, 밖이면 텍스트가 원본·캔버스는 read-only. 이 판정을 `coverage.ts` 한 곳에 둔다(단일 진실).

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `parseDslToIr(dsl): { ir: ModelIR | null; coverage: Coverage }` 구현.
- [ ] 지원 subset을 정확히 열거(§4.3) — `lazyfga-2` IR이 표현 가능한 구문과 정확히 일치.
- [ ] subset 밖 구문(예: `and`, `but not`, 비표준 userset, 조건)을 감지해 해당 relation을 `advanced`로 표시.
- [ ] `compileIrToDsl(parseDslToIr(dsl).ir)` 가 subset 내 모델에 대해 **의미 동등**함을 라운드트립 테스트로 보장.

### 3.2 Non-Goals

- [ ] 고급 구문을 IR로 표현하도록 IR을 확장하는 것(조건은 `lazyfga-14`에서 별도).
- [ ] DSL 텍스트 자체 편집기(= web, `lazyfga-5`).

## 4. Technical Design

### 4.1 Architecture Overview

```
.fga DSL ──@openfga/frontend-utils(parse)──▶ AST
   AST ──classify(coverage)──▶ {supported, advanced[]}
   AST(supported 부분) ──map──▶ ModelIR
```

### 4.2 Data Model Changes

변경 없음.

### 4.3 Core Logic

**지원 subset(이 안에서만 IR 왕복·캔버스 편집 가능):**
1. `type user` (base).
2. group type: `member` 단일 relation, 타입 제한이 `[user]` 또는 `[<group>#member]` 조합.
3. resource type의 relation은 다음 셋 중 하나로만 분류 가능해야 함:
   - **parent(상속 소스):** `define X: [<resource 타입 1개 이상>]` 형태이고 다른 relation의 `... from X`에서 참조됨 → `ParentRef{relationName:X, parentTypes:[...]}`(다중 타입 허용, round-trip 가능).
   - **role(부여):** `define X: [<user|group#member> ...]` (직접 할당 only, 우변에 computed 없음) → `Role`.
   - **permission(검사):** `define can_X: <role 이름들을 or> [or can_X from <parentRel>]` → `Permission`. 우변은 **(a) 같은 타입의 role 이름들의 union** 과 **(b) 동명 permission의 from-parent** 항만 허용.
4. 허용 연산자: `or`(union), `from`(tuple-to-userset, 단 §3-(c)-(b) 형태로 제한).

**subset 밖(→ advanced, read-only):**
- `and`(intersection), `but not`(exclusion).
- permission 우변이 role union/from 형태를 벗어난 경우(예: 다른 permission 참조, 교차 타입 userset).
- `condition`(CEL) 사용(조건은 `lazyfga-14`에서 다룸).
- 그 외 분류 불가 relation.

**분류 알고리즘(relation 단위):**
1. DSL을 AST로 파싱.
2. 각 type/relation을 위 규칙으로 분류 시도.
3. 분류 성공한 부분만 IR로 매핑. 분류 실패(=고급) relation은 `coverage.advanced`에 `{type, relation, reason}`으로 누적.
4. `advanced`가 비어 있으면 `coverage.fullyRepresentable = true`(완전 왕복 가능). 하나라도 있으면 `false`(텍스트가 원본; UI는 모델 전체를 read-only로, 해당 relation을 하이라이트).

**불변식(라운드트립):** `coverage.fullyRepresentable === true` 인 DSL에 대해, `compileIrToDsl(ir).model` 은 원본 DSL의 `AuthorizationModel` JSON과 **의미 동등**해야 한다(관계 정의 집합이 동일). 단 포맷/정렬/주석은 보존하지 않는다(정규화).

### 4.4 Worked Example

`lazyfga-3`의 골든 DSL을 입력 → `coverage.fullyRepresentable=true`, `ir`은 `doc-folder-team.ir.json`과 동등. 반대로 `define editor: [user] but not banned` 가 섞이면 → `advanced=[{type:"document", relation:"editor", reason:"EXCLUSION"}]`, `fullyRepresentable=false`.

## 5. API Design

### 5-1. New / Modified

```ts
// packages/compiler/src/dsl-to-ir.ts
/**
 * DSL을 IR로 역변환하고, 비주얼 표현 가능 범위를 판정한다.
 * - ir: subset으로 표현된 모델(부분 매핑). 완전 표현 불가여도 매핑 가능한 부분은 채운다.
 * - coverage: 무엇이 advanced인지, 완전 왕복 가능한지.
 */
export function parseDslToIr(dsl: string): { ir: ModelIR | null; coverage: Coverage };

// packages/compiler/src/coverage.ts
export interface Coverage {
  fullyRepresentable: boolean;
  advanced: Array<{ type: string; relation: string; reason: CoverageReason }>;
}
export type CoverageReason =
  | "INTERSECTION" | "EXCLUSION" | "CONDITION"
  | "NON_ROLE_UNION" | "CROSS_TYPE_USERSET" | "UNCLASSIFIABLE";
```

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| DSL 구문 오류(파싱 실패) | `ir=null`, `coverage.fullyRepresentable=false`, 파서 에러 메시지 동반 |
| 부분적으로만 표현 가능 | `ir`은 표현 가능 부분, `coverage.advanced`에 나머지 누적(예외 아님) |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                       | Estimated | Owner |
|---------|------------------------------------------------------------|-----------|-------|
| Phase 1 | DSL 파싱(AST) + relation 분류기(role/permission/parent)     | 1.5d      | TBD   |
| Phase 2 | coverage 판정(advanced reason 분류) + IR 매핑               | 1d        | TBD   |
| Phase 3 | 라운드트립 테스트(`compile(parse(dsl)) ≡ dsl`, subset 한정) | 1d        | TBD   |

### 6-2. Dependencies

- `@openfga/frontend-utils` (DSL 파서/AST).
- `packages/shared`(`ModelIR`), `packages/compiler`(`compileIrToDsl` — 라운드트립 검증용).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §"비주얼이 표현하는 범위"
- 메모리: `lazyfga-concept-logical-gaps` #2/#3 (경계 미설정 모순)
- [openfga/frontend-utils](https://github.com/openfga/frontend-utils)
