# Condition to CEL + Model/Policy Integration - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-29                       |
| Status     | **Draft**                        |
| Reviewers  |                                  |

---

## 1. Summary

`lazyfga-13`이 만든 `ConditionDef`(조건 트리)를 OpenFGA CEL로 양방향 변환하고, 모델 IR·컴파일러·발행·정책 경로에 통합한다. 조건은 **역할 부여(type-restriction) 레벨**에 붙는다(`define viewer: [user with non_expired]`). 이로써 M5 조건 기능(`lazyfga-13` 빌더 + 본 명세 통합)이 완성된다.

## 2. Background & Motivation

- `lazyfga-13`은 조건 저작 UI와 `ConditionDef` 계약까지만 만들고, OpenFGA 반영은 의도적으로 본 명세로 미뤘다.
- OpenFGA의 CEL condition은 **directly assignable relation의 type restriction에만** 붙는다(computed relation엔 직접 못 붙음). 따라서 조건은 `Permission`(computed `can_<name>`)이 아니라 `Role.assignableBy`의 주체(type restriction)에 부착해야 한다 — `lazyfga-2` §3.2의 배치 권고이자 사용자 확정(Q1=A).
- `Permission.condition`(`lazyfga-2`)·`Policy.conditionRef`(`lazyfga-8`)는 "배치 미정" 상태로 예약돼 있었다. 본 명세가 그 예약을 **확정 해소**한다(아래 §4.2).

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `ModelIR` 확장(Q1=A): 최상위 `conditions?: ConditionDef[]` + `SubjectRef`에 선택 `condition?: string`(조건명 참조). `Permission.condition`/`ConditionRef`는 제거한다.
- [ ] `packages/compiler/src/condition-to-cel.ts`: `conditionToCel(def)` (정방향: `ConditionDef` → `condition <name>(params) { CEL }`) + `tryParseCondition(...)` (역방향: 우리가 생성한 제한 subset만 `ConditionDef`로 복원).
- [ ] `ir-to-dsl`: 최상위 `condition` 블록 + 조건부 type restriction(`[user with cond]`) 방출. 골든 byte 왕복 유지.
- [ ] `dsl-to-ir` + `coverage`: 생성 subset에 해당하는 조건은 IR로 복원, 그 밖의 임의 CEL은 advanced(read-only)로 표시.
- [ ] `validateModelIR` 갱신: `CONDITION_RESERVED` 제거, 조건 참조·정의·이름 유일성 검증 추가.
- [ ] `shared/edit.ts` 순수 편집 연산: `addCondition`/`updateCondition`/`renameCondition`/`removeCondition` + `setAssignmentCondition`(특정 `SubjectRef`에 조건 부착/해제).
- [ ] web: `lazyfga-13`의 `ConditionBuilder`를 역할 부여 편집(`assignableBy`)에 연결, 모델 단위 조건 목록 관리, DSL 미리보기에 조건 반영.
- [ ] 정책 통합: `policyContextParams(ir, policy)` 파생 헬퍼(정책 평가에 필요한 context 파라미터 산출). evaluate의 context 통과는 `lazyfga-9`에 이미 구현됨 — 검증·문서화.

### 3.2 Non-Goals

- [ ] 임의 CEL 저작/완전 왕복. 우리가 생성한 제한 문법(시간·IP·값 leaf의 단일 `and`/`or` 그룹)만 양방향이고, 그 밖의 CEL은 advanced(read-only).
- [ ] 중첩 그룹 빌더 UI(`lazyfga-13` non-goal 유지).
- [ ] 모델 조건과 별개의 정책 단위(per-policy) 조건. 조건은 모델 레벨에서만 강제되고 정책은 context를 통과만 시킨다.
- [ ] 조건 평가(런타임). OpenFGA가 Check 시 수행.

## 4. Technical Design

### 4.1 Architecture Overview

```
web (assignableBy 편집 + 조건 목록)
   └─ ConditionBuilder(lazyfga-13) → ConditionDef
   └─ setAssignmentCondition → SubjectRef.condition = "<name>"

ModelIR { conditions: ConditionDef[], resources[].roles[].assignableBy[].condition? }
   ── compileIrToDsl ──▶  condition <name>(...) { CEL }  +  define viewer: [user with <name>]
   ◀─ parseDslToIr ───  (생성 subset만 복원; 그 밖 CEL → coverage advanced)

발행: model.service → compileIrToDsl → OpenFGA WriteAuthModel(conditions 포함)
평가: PEP → evaluate(context) → OpenFGA Check가 CEL 평가 (lazyfga-9 경로, 변경 없음)
```

### 4.2 Data Model Changes

**IR 타입(코드 변경).**

```ts
// packages/shared/src/model.ts
export type SubjectRef =
  | { kind: "user"; condition?: string }
  | { kind: "group"; group: string; relation: "member"; condition?: string };

export interface ModelIR {
  schemaVersion: "1.1";
  groups: GroupType[];
  resources: ResourceType[];
  conditions?: ConditionDef[];   // 신규: 최상위 조건 정의(OpenFGA `condition` 블록과 1:1)
}
// 제거: Permission.condition, interface ConditionRef
```

**예약 해소(중요).**
- `Permission.condition`/`ConditionRef`: 제거(조건은 computed permission이 아니라 role 부여에 붙으므로 불필요). 검증 코드 `CONDITION_RESERVED`도 제거.
- `Policy.conditionRef`(DB `policy.condition_ref`): 조건은 모델 레벨에서 OpenFGA가 강제하므로 **정책 단위 조건은 도입하지 않는다.** 컬럼/필드는 그대로 두되 MVP 미사용으로 문서화한다(불필요한 마이그레이션 회피). 정책이 필요로 하는 context는 `policyContextParams`로 모델에서 파생한다. **이 결정은 `lazyfga-8` §3.2의 "condition을 정책에 선택 결합" 예약을 대체한다**(해당 절에 supersede 주석 추가).

**zod 런타임 스키마(`model.ts`)도 갱신.** `subjectRefSchema`의 두 변형에 `condition: z.string().optional()` 추가, `conditionRefSchema`·`modelIrSchema.permissions[].condition` 제거, 최상위 `conditions`를 `lazyfga-13`의 `conditionDefSchema` 배열(`.optional()`)로 추가.

**DB.** `model_version.ir_json`(jsonb)는 이제 `conditions`를 포함할 수 있다 — 스키마 변경 없음. 신규 테이블 없음.

### 4.3 Core Logic

**정방향 `conditionToCel(def): { decl: string; cel: string }` (결정적).**
- 헤더: `condition <name>(<p1>: <type1>, <p2>: <type2>, ...)` — 파라미터는 `params` 선언 순서 그대로.
- 타입 매핑: `timestamp`→`timestamp`, `ipaddress`→`ipaddress`, `string`/`int`/`double`/`bool`→동명 CEL 타입.
- leaf → CEL:
  - `time`: `<param> <op> <rhs>` (`lt`→`<`, `lte`→`<=`, `gt`→`>`, `gte`→`>=`; rhs literal은 `timestamp("<rfc3339>")`, param은 식별자).
  - `ip`: `<param>.in_cidr("<cidr>")`.
  - `value`: `<param> <op> <literal>` (`eq`→`==`, `neq`→`!=`, 나머지 부등호; string은 큰따옴표, int/double/bool은 리터럴).
- 그룹: children CEL을 ` && `(and)/` || `(or)로 결합, child≥2면 괄호. 결과: `condition <name>(...) { <body> }`.

**조건부 type restriction 방출(`ir-to-dsl`).** `SubjectRef`에 `condition`이 있으면 `user` → `user with <cond>`, `<group>#member` → `<group>#member with <cond>`. 한 relation의 restriction 목록은 조건부/무조건부 혼합 가능(`[user, user with non_expired]`). `condition` 블록은 type 정의들 뒤에 최상위로 방출한다(OpenFGA DSL 문법).

**역방향 `tryParseCondition` + coverage(`dsl-to-ir`).** OpenFGA model JSON의 condition 정의(파라미터 타입 + CEL 본문)를 읽어, 본문이 **우리 생성 문법**(선언된 param에 대한 시간/IP/값 leaf를 단일 `&&` 또는 `||`로 결합)에 부합하면 `ConditionDef`로 복원하고 해당 `with <cond>`를 `SubjectRef.condition`으로 복원한다. 부합하지 않는 임의 CEL은 복원하지 않고 `coverage`를 advanced(`fullyRepresentable=false`)로 표시한다(모델은 계속 시각화, 편집은 read-only). **기존 `CoverageReason: "CONDITION"`(`coverage.ts`)을 그대로 쓰고 방출 시점만 바꾼다**: 현재는 condition이 붙은 모든 ref를 무조건 advanced로 처리하나(`dsl-to-ir.ts` L80·L182, `isPlainRef` L51), 생성 subset에 부합하면 advanced로 보내지 않고 IR로 복원한다.

**coverage 갱신 지점(명시).**
1. `coverage.ts`의 `fullyRepresentable` 전제 "조건 없음"을 "비-subset 조건 없음"으로 완화(생성 subset 조건은 완전 왕복 가능).
2. `dsl-to-ir.ts` L225-227의 모델 레벨 `notes`는 **복원 못 한** condition에 대해서만 방출한다(subset 복원 성공 시 note 없음).
3. `isPlainRef`(L51)·group member 분기(L182)·`classifyDirect`(L80)를 subset 조건부 ref를 허용하도록 완화한다. `RelationRefJSON.condition`은 이미 파싱된다.

**`validateModelIR` 갱신.**
- 제거: `CONDITION_RESERVED`(이를 단언하던 기존 테스트 `packages/shared/src/model.test.ts`도 제거/대체).
- 신규 `CONDITION_UNKNOWN`: `SubjectRef.condition`이 `ModelIR.conditions[].name`에 없으면.
- 신규 `DUP_CONDITION`: `conditions` 내 이름 중복.
- 각 `ConditionDef`는 `validateConditionDef`(`lazyfga-13`)로 검증하고, 그 `ConditionError`를 모델 `ValidationError`(path 접두 `conditions[i]`)로 승격(승격을 위해 `ValidationError.code`를 `ConditionErrorCode` 포함으로 확장 — §5-1). 조건명은 기존 식별자/예약어 규칙(rule 1) 적용.

**정책 통합 `policyContextParams(ir, policy): ConditionParam[]`.** 정책 `(permission P, resourceType T)`에 대해: `T`(및 상속 부모, 깊이 한도)에서 `can_P`를 부여하는 role들의 `assignableBy` 중 `condition`이 붙은 것들을 모아, 해당 조건의 `params`를 합집합으로 반환(name 기준 dedup; 같은 이름이 타입 충돌하면 오류 표시). playground(`lazyfga-18`)·PEP가 "이 정책엔 어떤 context를 넣어야 하나"를 알 수 있게 한다. 결정 자체는 OpenFGA Check가 context로 수행(evaluate 변경 없음).

### 4.4 발행/평가 영향

- **발행(`lazyfga-7`)**: API/스키마 변경 없음. `compileIrToDsl`가 조건을 포함한 DSL/모델 JSON을 만들고 `WriteAuthModel`에 그대로 전달한다. 무효 조건(검증 실패)은 발행 전 422(`PublishError`).
- **평가(`lazyfga-9`)**: 변경 없음. 요청 `context`가 이미 OpenFGA Check로 통과된다. 두 경우를 **구분**한다: ① context 값이 주어졌고 조건이 false면 정상 **deny**(`decision:false`); ② 조건이 요구하는 context 키가 **누락**되면 OpenFGA가 평가 오류를 내고 이는 `lazyfga-9` 경로에서 **500**으로 표면화된다(조용한 deny가 아님). 이 차이를 문서화한다.

## 5. API Design

### 5-1. New / Modified

```ts
// packages/compiler/src/condition-to-cel.ts
/** ConditionDef → OpenFGA condition 선언/본문(결정적). */
export function conditionToCel(def: ConditionDef): { decl: string; cel: string };
/** 우리가 생성한 제한 subset의 CEL만 ConditionDef로 복원. 그 밖이면 null. */
export function tryParseCondition(
  name: string, params: ConditionParam[], celBody: string,
): ConditionDef | null;

// packages/shared/src/model.ts  — SubjectRef.condition, ModelIR.conditions(위 §4.2),
//   ValidationErrorCode += "CONDITION_UNKNOWN" | "DUP_CONDITION"; "CONDITION_RESERVED" 제거.
//   승격되는 ConditionError(lazyfga-13)를 담기 위해:
//     ValidationError["code"] = ValidationErrorCode | ConditionErrorCode  (BAD_NAME은 공통 — 무해)
//   즉 TYPE_MISMATCH/BAD_CIDR/EMPTY_GROUP 등 6개 ConditionErrorCode도 모델 ValidationError로 표현 가능.

// packages/shared/src/edit.ts
export function addCondition(ir: ModelIR, def: ConditionDef): ModelIR;
export function updateCondition(ir: ModelIR, name: string, def: ConditionDef): ModelIR;
export function renameCondition(ir: ModelIR, from: string, to: string): ModelIR; // SubjectRef 참조도 갱신
export function removeCondition(ir: ModelIR, name: string): ModelIR;             // 참조 SubjectRef.condition 정리
// subjectIndex는 같은 IR 스냅샷의 assignableBy 배열 인덱스. 범위 밖/대상 없음이면 ir 그대로 반환(edit 관례).
// name이 아닌 index인 이유: [user with a, user with b]처럼 동일 base 주체가 중복 가능하기 때문.
export function setAssignmentCondition(
  ir: ModelIR, typeName: string, role: string, subjectIndex: number, condition: string | null,
): ModelIR;

// packages/shared/src/policy.ts (또는 pdp helper)
export function policyContextParams(ir: ModelIR, policy: Policy): ConditionParam[];
```

`compileIrToDsl`/`parseDslToIr` 시그니처는 불변, 동작만 확장. 신규 REST 없음(`/model` 발행, `/access/v1/evaluation` 그대로).

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| `SubjectRef.condition`가 미정의 조건 참조 | `CONDITION_UNKNOWN`(인라인) |
| `conditions` 이름 중복 | `DUP_CONDITION` |
| `ConditionDef` 자체 무효 | `validateConditionDef` 코드 → 모델 `ValidationError`로 승격 |
| 무효 조건 포함 모델 발행 | 422 `PublishError`(발행 차단, `model.service`) |
| 임의 CEL(생성 subset 외) import | `coverage` advanced(read-only), 예외 아님 |
| 평가 시 조건 context 값 주어짐 + false | 정상 deny(`decision:false`) |
| 평가 시 조건 context 키 누락 | OpenFGA 평가 오류 → 500(`lazyfga-9` 경로, 조용한 deny 아님) |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                              | Estimated | Owner |
|---------|-----------------------------------------------------------------------------------|-----------|-------|
| Phase 1 | IR 확장 + `validateModelIR` 갱신 + edit 연산 + `conditionToCel`(정방향) + `ir-to-dsl` 방출 + 골든 테스트 | 1.5d      | TBD   |
| Phase 2 | `tryParseCondition`(역방향) + `dsl-to-ir`/`coverage` + 왕복 테스트                   | 1d        | TBD   |
| Phase 3 | web 연결(assignableBy에 조건 부착 + 조건 목록 + DSL 미리보기) + `policyContextParams` + 발행/평가 검증 + E2E | 1.5d      | TBD   |

### 6-2. Dependencies

- `lazyfga-13`(`ConditionDef`/`describeCondition`/`validateConditionDef`, `ConditionBuilder`), `lazyfga-3`/`lazyfga-4`(컴파일러·coverage), `lazyfga-7`(발행), `lazyfga-8`(정책), `lazyfga-9`(evaluate context 통과).
- `@openfga/syntax-transformer`(0.2.1): `condition` 블록을 **양방향 모두 지원함**(확인 완료, 업그레이드 불필요). JSON 형태는 `conditions[name] = { expression, parameters: { <p>: { type_name: "TYPE_NAME_*" } } }`이므로, `dsl-to-ir`는 `TYPE_NAME_*` → `ConditionParamType` 매핑 후 `expression`을 `tryParseCondition`의 `celBody`로 전달한다(그래서 `tryParseCondition`은 변환 완료된 `ConditionParam[]`를 받는다).

## 7. References

- [OpenFGA Conditions](https://openfga.dev/docs/modeling/conditions) · [Conditions in the DSL](https://openfga.dev/docs/configuration-language#conditions)
- `lazyfga-13`(조건 빌더·계약), `lazyfga-2`(`Permission.condition` 예약·배치 권고), `lazyfga-8`(`Policy.conditionRef` 예약), `lazyfga-9`(evaluate context 통과)
- [openfga/language (syntax-transformer)](https://github.com/openfga/language)
