# Condition Builder UI (WAF-style And/Or blocks) - Spec Proposal

| Item      | Detail                                                              |
| --------- | ------------------------------------------------------------------- |
| Author    | Seonguk Moon                                                        |
| Created   | 2026-06-29                                                          |
| Status    | **Implemented**                                                     |
| Reviewers | Claude (M5 cross-review + adversarial re-review; Codex unavailable) |

---

## 1. Summary

Cloudflare WAF식 `AND`/`OR` 블록 UI로 속성 조건(시간·IP·요청값)을 조립하는 비주얼 빌더와, 그 결과를 담는 `ConditionTree`/`ConditionDef` 데이터 계약을 정의한다. 빌더가 만든 조건은 사람이 읽는 미리보기로 즉시 확인된다. OpenFGA CEL로의 컴파일과 모델/정책 통합은 `lazyfga-14`가 맡는다. (CONCEPT §2 "WAF식 조건 빌더")

## 2. Background & Motivation

- CONCEPT 차별 기둥의 하나: 속성 기반(ABAC) 규칙을 코드 없이, 비개발자도 읽고 만들 수 있게 한다(§2).
- 현재 조건 기능은 미구현이다: `packages/shared/src/condition.ts`는 스텁(`export {}`)이고, `Permission.condition`(`lazyfga-2`)·`Policy.conditionRef`(`lazyfga-8`)는 **예약 필드**로만 존재한다.
- OpenFGA의 [CEL condition](https://openfga.dev/docs/modeling/conditions)을 텍스트로 직접 쓰는 학습 부담을, 선형 블록 조립으로 대체한다.
- **이 빌더가 만드는 건 속성 조건이지 권한 로직이 아니다(중요).** 누가 무엇을 할 수 있는지는 행렬(`lazyfga-6`)이 정하고, 조건은 그 위에 _"단, ~할 때만"_ 을 얹는다. 조건이 역할 부여(type-restriction)에 부착된다는 결정(Q1)은 `lazyfga-14`에서 IR/컴파일러에 반영한다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `packages/shared/src/condition.ts`에 `ConditionTree`(`AND`/`OR` 그룹 + leaf 술어)·`ConditionDef`(name·params·tree) 계약 + zod 스키마 정의.
- [ ] 지원 피연산자(MVP, Q2 확정): `timestamp`(시간 윈도우), `ipaddress`(CIDR 포함), 일반 값 비교(`string`/`int`/`double`/`bool` 의 `==`,`≠`,`<`,`≤`,`>`,`≥`). **모두 OpenFGA 네이티브 condition 파라미터 타입에 1:1 대응한다 — lazyFGA는 조건을 직접 평가하지 않는다.**
- [ ] `describeCondition(node): string` — 조건 트리를 사람이 읽는 한 줄로 렌더(순수·isomorphic).
- [ ] `validateConditionDef(def): ConditionError[]` — 정적 검증(예외 없이 위반 수집).
- [ ] web `features/condition-builder`: 선형 `AND`/`OR` 블록 편집기 + 피연산자 카탈로그 + 파라미터 관리 + 미리보기. 재사용 컴포넌트로 조건 1개(`ConditionDef`)를 `value`/`onChange`로 다룬다.

### 3.2 Non-Goals

- [ ] `ConditionTree` → CEL 컴파일(`compiler/condition-to-cel.ts`), `ir-to-dsl`/`dsl-to-ir` 반영, `validateModelIR` 완화(`CONDITION_RESERVED`), 모델 발행, 정책 바인딩 — 전부 `lazyfga-14`.
- [ ] 조건을 역할 부여(type-restriction)에 실제로 부착·영속하는 IR/스토어 통합 — `lazyfga-14`(Q1=A: `SubjectRef`에 조건명을 붙이는 형태).
- [ ] 중첩 그룹 빌더 UI — 계약(`ConditionNode`)은 중첩을 허용하되, MVP UI는 **단일 그룹 선형**(CONCEPT "선형으로 쌓아")으로 렌더한다.
- [ ] 조건 평가(런타임). lazyFGA는 CEL을 생성·선언만 하고, 평가는 OpenFGA가 Check 시점에 수행한다(파라미터 값은 AuthZEN `context`로 주입; `lazyfga-14`가 기존 `lazyfga-9` PDP 경로를 확장).

## 4. Technical Design

### 4.1 Architecture Overview

```
web/features/condition-builder
  ConditionBuilder(value: ConditionDef, onChange)
     ├─ ParamPanel : ConditionParam[] 편집(이름·타입)
     ├─ RuleList   : ConditionLeaf 행들 + 그룹 combinator(and|or)
     │                각 행 = 종류(time|ip|value) → param 선택 + op + 값
     └─ Preview    : describeCondition(tree)  사람이 읽는 미리보기

packages/shared/src/condition.ts
     ConditionTree/ConditionDef 계약 + describeCondition + validateConditionDef (순수)
```

조건의 흐름(평가는 OpenFGA가 한다):

```
빌더 → ConditionDef ──(lazyfga-14)──▶ CEL codegen → OpenFGA 모델의 `condition` 블록
요청 시: AuthZEN context(런타임 값) ──▶ OpenFGA Check가 CEL 평가
```

### 4.2 Data Model Changes

DB 변경 없음. `shared`의 타입 계약만 추가(`condition.ts` 스텁 → 실제 타입). `ModelIR` 확장(조건을 역할 부여에 부착)은 `lazyfga-14` 범위.

### 4.3 Core Logic

**피연산자 카탈로그(MVP) → OpenFGA 네이티브 대응.** 각 leaf는 OpenFGA가 직접 이해하는 타입/연산으로만 컴파일된다(`lazyfga-14`). lazyFGA는 IP·시간 산술을 재구현하지 않는다.

| leaf 종류 | OpenFGA param 타입             | 연산                             | 생성될 CEL(참고, lazyfga-14)    |
| --------- | ------------------------------ | -------------------------------- | ------------------------------- |
| `time`    | `timestamp`                    | `lt`·`lte`·`gt`·`gte`            | `current_time < expiry`         |
| `ip`      | `ipaddress`                    | `in_cidr`                        | `user_ip.in_cidr("10.0.0.0/8")` |
| `value`   | `string`·`int`·`double`·`bool` | `eq`·`neq`·`lt`·`lte`·`gt`·`gte` | `tier == "gold"`                |

**파라미터(param)의 의미.** `ConditionParam`은 조건이 입력으로 받는 변수다(예: `current_time`, `user_ip`). 런타임 값은 PEP가 AuthZEN `context`로 전달하며 OpenFGA가 Check 시 바인딩한다. (OpenFGA는 `current_time`을 자동 주입하지 않으므로 시간 기반 조건은 호출자가 `context`에 현재 시각을 넣어야 한다 — 이 계약/검증은 `lazyfga-14`가 기존 `lazyfga-9`(PDP evaluate, 구현 완료) 경로를 확장해 다룬다.)

**트리 구조(WAF식, 선형).** 빌더 MVP는 단일 `ConditionGroup`(combinator `and` 또는 `or`) 아래에 leaf 행들을 선형으로 둔다. "업무시간 AND 사내 IP" = `{ op:"and", children:[time, ip] }`. 계약 자체는 `ConditionNode`(그룹 또는 leaf)로 중첩을 허용한다. 빌더 MVP는 root를 **항상 단일 `ConditionGroup`으로 정규화**한다(조건 1개 = child 1개 그룹). `value`로 들어온 트리의 root가 bare leaf면 단일 그룹으로 감싸 편집하고, **중첩 그룹이 포함된 트리가 들어오면 MVP는 read-only로 표시(편집 비활성)하고 `describeCondition` 미리보기만 제공**한다(데이터 손상 방지). 중첩 그룹 편집 UI는 후속이다.

**`describeCondition` 규칙(순수, 결정적).**

- `time` → `"<param> {연산기호} {rfc3339|param}"` 예: `current_time < expiry`.
- `ip` → `"<param> in {cidr}"` 예: `user_ip in 10.0.0.0/8`.
- `value` → `"<param> {연산기호} {리터럴}"` (문자열은 따옴표).
- `group` → children 설명을 `AND`/`OR`로 결합한다. child가 2개 이상이면 괄호로 감싸고, 중첩 그룹은 자식 그룹을 먼저 괄호로 감싼 뒤 결합한다(and/or 우선순위 모호성 제거). 빈 그룹은 `"(empty)"`.

**`validateConditionDef` 규칙(빈 배열 = 유효, 예외 없음).** 괄호 안은 발생 코드.

1. `name`·각 `param.name`은 식별자 규칙(`^[a-zA-Z0-9_]+$`)이며 예약어와 충돌하지 않는다(`BAD_NAME`). `model.ts`의 `IDENT_RE`·`RESERVED_WORDS`를 `shared`에서 export해 **단일 출처로 재사용**한다 — `shared` 내부 변경이며 컴파일러/IR과 무관.
2. `params`의 이름은 유일하다(`DUP_PARAM`).
3. 모든 leaf의 `param`은 `params`에 선언돼 있고(`UNKNOWN_PARAM`), leaf 종류와 param 타입이 일치한다(`TYPE_MISMATCH`): `time`↔`timestamp`, `ip`↔`ipaddress`, `value`↔`string|int|double|bool`.
4. `time.rhs.kind==="param"`이면 그 param도 선언돼 있고(`UNKNOWN_PARAM`) `timestamp` 타입이어야 한다(`TYPE_MISMATCH`).
5. `value` leaf의 리터럴은 참조 param 타입과 일치해야 한다(`TYPE_MISMATCH`): `string`→문자열, `int`→정수, `double`→유한 수, `bool`→불리언. 또한 순서 비교(`lt`/`lte`/`gt`/`gte`)는 `bool` param에 쓸 수 없다(`TYPE_MISMATCH`).
6. `ip.cidr`는 CIDR 형식(IPv4/IPv6 `addr/prefix`, `BAD_CIDR`), `time.rhs.rfc3339`는 RFC3339 형식(`BAD_TIMESTAMP`).
7. 그룹은 child가 1개 이상이어야 한다(`EMPTY_GROUP`).

UI는 위반을 인라인으로 표시하며, 검증 실패 상태의 조건은 `lazyfga-14`의 발행 단계에서 거부된다(빌더는 저장 가능하지만 무효 표시).

## 5. API Design

### 5-1. New / Modified

신규 REST 없음. `shared` 타입 계약 + web 컴포넌트 계약.

```ts
// packages/shared/src/condition.ts  (스텁 대체; lazyfga-0 shared index에 이미 ./condition 등록)

/** 조건 파라미터 타입. OpenFGA 네이티브 condition 파라미터 타입의 부분집합(MVP). */
export type ConditionParamType = "timestamp" | "ipaddress" | "string" | "int" | "double" | "bool";

export interface ConditionParam {
  name: string; // CEL 파라미터 이름. 예: "current_time", "user_ip"
  type: ConditionParamType;
}

export type TimeRhs =
  | { kind: "literal"; rfc3339: string } // 고정 시각
  | { kind: "param"; param: string }; // 다른 timestamp 파라미터(예: expiry)

/** 단일 비교(leaf 술어). */
export type ConditionLeaf =
  | { kind: "time"; param: string; op: "lt" | "lte" | "gt" | "gte"; rhs: TimeRhs }
  | { kind: "ip"; param: string; op: "in_cidr"; cidr: string }
  | {
      kind: "value";
      param: string;
      op: "eq" | "neq" | "lt" | "lte" | "gt" | "gte";
      value: string | number | boolean;
    };

/** AND/OR 그룹(WAF식). children는 leaf 또는 중첩 그룹. */
export interface ConditionGroup {
  op: "and" | "or";
  children: ConditionNode[];
}
export type ConditionNode = ConditionGroup | ConditionLeaf;

/** 이름 붙은 재사용 조건. OpenFGA `condition <name>(<params>) { <CEL> }` 한 블록에 대응. */
export interface ConditionDef {
  name: string;
  params: ConditionParam[];
  tree: ConditionNode;
}

export const conditionDefSchema: import("zod").ZodType<ConditionDef>;

/** 조건 트리를 사람이 읽는 한 줄로 렌더(순수). */
export function describeCondition(node: ConditionNode): string;

export type ConditionErrorCode =
  | "BAD_NAME"
  | "DUP_PARAM"
  | "UNKNOWN_PARAM"
  | "TYPE_MISMATCH"
  | "BAD_CIDR"
  | "BAD_TIMESTAMP"
  | "EMPTY_GROUP";
export interface ConditionError {
  code: ConditionErrorCode;
  path: string;
  message: string;
}

/** 정적 검증(빈 배열 = 유효). */
export function validateConditionDef(def: ConditionDef): ConditionError[];
```

```ts
// apps/web/src/features/condition-builder  (재사용 컴포넌트)
export function ConditionBuilder(props: {
  value: ConditionDef;
  onChange(next: ConditionDef): void;
}): JSX.Element;
```

### 5-2. Error Handling

REST 아님. `validateConditionDef`가 수집하는 케이스:

| 상황                                                                                                     | 처리                         |
| -------------------------------------------------------------------------------------------------------- | ---------------------------- |
| 잘못된 name/param 이름, 예약어 충돌                                                                      | `BAD_NAME` (인라인 표시)     |
| param 이름 중복                                                                                          | `DUP_PARAM`                  |
| leaf(또는 `time.rhs`)가 미선언 param 참조                                                                | `UNKNOWN_PARAM`              |
| leaf 종류 ↔ param 타입 불일치, `value` 리터럴/순서비교 ↔ param 타입 불일치, `time.rhs` param 타입 불일치 | `TYPE_MISMATCH`              |
| CIDR/RFC3339 형식 오류                                                                                   | `BAD_CIDR` / `BAD_TIMESTAMP` |
| 빈 그룹                                                                                                  | `EMPTY_GROUP`                |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                   | Estimated | Owner |
| ------- | -------------------------------------------------------------------------------------- | --------- | ----- |
| Phase 1 | `condition.ts` 계약 + zod + `describeCondition` + `validateConditionDef` + 단위 테스트 | 1d        | TBD   |
| Phase 2 | `ConditionBuilder` 컴포넌트(ParamPanel + RuleList + combinator)                        | 1.5d      | TBD   |
| Phase 3 | `describeCondition` 미리보기 + 인라인 검증 표시 + 사용 예시 마운트                     | 0.5d      | TBD   |

### 6-2. Dependencies

- `lazyfga-5`(model-canvas; 빌더의 최종 마운트는 `lazyfga-14`에서 역할 부여 편집과 결합), `packages/shared`, `zod`, React. **컴파일러 의존 없음**(CEL 변환은 `lazyfga-14`).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §2 WAF식 조건 빌더
- [OpenFGA Conditions](https://openfga.dev/docs/modeling/conditions) · [Condition parameter types](https://openfga.dev/docs/modeling/conditions#supported-parameter-types)
- [Common Expression Language (CEL)](https://github.com/google/cel-spec)
- `lazyfga-2`(`Permission.condition` 예약·배치 재검토 주석), `lazyfga-14`(condition → CEL + 모델/정책 통합)
