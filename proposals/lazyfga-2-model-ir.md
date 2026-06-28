# Model IR (5-primitive) - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M1 cross-review)  |

---

## 1. Summary

비주얼 캔버스와 OpenFGA DSL 사이의 중간 표현(Intermediate Representation, IR)을 정의한다. IR은 CONCEPT.md의 5 primitive(Resource·Role·Permission·Hierarchy·Group)를 그대로 데이터 구조로 옮긴 것으로, 캔버스·행렬 UI가 읽고 쓰며, 컴파일러(`lazyfga-3/4`)가 OpenFGA DSL과 양방향 변환하는 단일 계약이다.

## 2. Background & Motivation

- OpenFGA DSL을 직접 다루면 `from`(tuple-to-userset)·`#`(userset)·union 등 입문 장벽이 그대로 노출된다(CONCEPT §"지금, 인가는 이렇게 한다").
- 캔버스/행렬은 "타입·역할·권한·상속·그룹"으로 사고한다. 이를 **DSL과 독립된 안정적 IR**로 고정하면, UI와 컴파일러가 느슨하게 결합되고, "비주얼 표현 범위"(CONCEPT)도 IR 표현 가능 여부로 정의된다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `packages/shared/src/model.ts`에 `ModelIR` 및 하위 타입을 정의(타입 + 런타임 검증 스키마).
- [ ] 각 필드의 의미를 OpenFGA 구문과 1:1로 대응시켜 명문화(오해 불가).
- [ ] CONCEPT의 document/folder/team 예시를 IR 인스턴스로 표현한 레퍼런스 픽스처 제공.
- [ ] IR 유효성 검증 함수(`validateModelIR`)의 규칙 정의.

### 3.2 Non-Goals

- [ ] IR→DSL, DSL→IR 변환 알고리즘(= `lazyfga-3`, `lazyfga-4`).
- [ ] 조건(condition/CEL) 표현(= `lazyfga-14`에서 IR 확장). 본 명세의 IR에는 조건 필드를 **예약만** 해둔다. (주의: OpenFGA CEL condition은 assignable relation의 type restriction에 붙으므로, `lazyfga-14`에서 condition을 computed `Permission`이 아니라 `Role.assignableBy`/type-restriction 레벨로 배치 검토.)
- [ ] **역할 계층(role hierarchy, owner⊇editor⊇viewer)** 은 MVP 비목표. 권한은 행렬(permission.grantedByRoles)로 **명시적**으로만 표현한다. (2026-06-28 확정 — §4.3 주석 참고)

## 4. Technical Design

### 4.1 Architecture Overview

IR은 순수 데이터다. 소비자/생산자:
```
web/model-canvas, web/permission-matrix  ── read/write ──▶  ModelIR
packages/compiler (ir-to-dsl / dsl-to-ir) ── transform ──▶  OpenFGA DSL/JSON
```

### 4.2 Data Model Changes

DB 변경 없음(IR은 코드 타입). IR 자체는 모델 발행 시 OpenFGA에 DSL로 반영되고, 버전 메타는 `lazyfga-7`에서 Postgres에 저장.

### 4.3 Core Logic

IR 스키마(개념상 오해가 없도록 각 필드의 DSL 대응을 명시):

```ts
// packages/shared/src/model.ts
export interface ModelIR {
  schemaVersion: "1.1";          // OpenFGA schema 버전 고정
  groups: GroupType[];           // 주체(subject) 묶음 — 예: team
  resources: ResourceType[];     // 보호 대상 — 예: folder, document
}

// 주체 그룹. DSL: `type <name> { relations { define member: [<memberTypes>] } }`
export interface GroupType {
  name: string;                          // 예: "team"  → OpenFGA type 이름
  memberTypes: SubjectRef[];             // member 관계의 type restriction. 예: [user, team#member]
}

export interface ResourceType {
  name: string;                  // 예: "document" → OpenFGA type
  parents: ParentRef[];          // 상속(Hierarchy). 없으면 빈 배열
  roles: Role[];                 // 부여(assignable) 가능한 관계
  permissions: Permission[];     // 검사(computed) 관계. DSL에서 `can_<name>`
}

// 상속 엣지. DSL: `define <relationName>: [<parentTypes...>]` + 권한이 `... from <relationName>`
export interface ParentRef {
  relationName: string;          // 예: "parent". ResourceType 내 relation 네임스페이스(role/permission/parent) 전역 유일
  parentTypes: string[];         // 예: ["folder"] 또는 ["folder","document"]. 모두 resources 내 존재. 같은 relationName은 단일 ParentRef로 병합
}

// 부여 가능한 역할. DSL: `define <name>: [<assignableBy>]`
export interface Role {
  name: string;                  // 예: "owner" | "editor" | "viewer"
  assignableBy: SubjectRef[];    // type restriction. 예: [user, team#member]
}

// 검사용 권한(액션). DSL: `define can_<name>: <grantedByRoles를 or로> [or can_<name> from <parent>]`
export interface Permission {
  name: string;                  // 예: "read" → 관계 이름은 "can_read"
  grantedByRoles: string[];      // 이 권한을 주는 역할 목록(행렬의 열). 같은 ResourceType.roles[].name 중 하나 이상
  inheritFromParents: string[];  // 상속받을 ParentRef.relationName 목록(없으면 빈 배열)
  condition?: ConditionRef;      // 예약(= lazyfga-14, 배치 재검토). MVP에선 항상 undefined
}

// 주체 참조: 직접 user이거나, 그룹 멤버 userset
export type SubjectRef =
  | { kind: "user" }                       // DSL: user
  | { kind: "group"; group: string; relation: "member" }; // DSL: <group>#member

export interface ConditionRef { name: string } // 예약
```

> **역할 계층 비목표 근거 (2026-06-28 확정):** CONCEPT은 owner⊇editor⊇viewer 같은 ranking을 "편의"로 언급했으나, 동일 의미를 행렬로 완전히 표현할 수 있다(예: read 열에 viewer·editor·owner 모두 체크). MVP는 행렬을 **유일한 권한 출처**로 삼아 단순성을 택한다. 역할→역할 함의(`implies`)는 후속 확장 여지로만 둔다. — 사용자 확정: 행렬 방식 채택.

검증 규칙(`validateModelIR(ir): Result<void, ValidationError[]>`):
1. 모든 `name`은 OpenFGA 식별자 규칙(`^[a-zA-Z0-9_]+$`)을 만족하고 예약어와 충돌하지 않는다.
2. type 이름(groups+resources)은 전역 유일. `user`는 예약 base type이므로 사용자 정의 금지.
3. 한 ResourceType 내 **relation 네임스페이스 전역**(role 이름, `can_<permission>`, `ParentRef.relationName`)이 서로 유일하다 — role·permission·parent relation 이름이 충돌하지 않는다.
4. `ParentRef.parentTypes`의 각 값은 `resources`에 존재. `Permission.inheritFromParents`의 각 값은 같은 ResourceType의 `parents[].relationName`에 존재.
5. `Permission.grantedByRoles`의 각 값은 같은 ResourceType의 `roles[].name`에 존재(빈 배열 금지 — 아무도 못 가지는 권한은 무의미).
6. `SubjectRef.group`은 `groups`에 존재.
7. `Permission.inheritFromParents`로 상속받는 각 부모 relation에 대해, 그 `parentTypes`의 **모든** 타입이 동명 permission(`can_<perm>`)을 가져야 한다 — 없으면 `can_<perm> from <rel>`(tuple-to-userset)이 OpenFGA에서 무효.
8. 같은 `relationName`의 `ParentRef`는 ResourceType 내 최대 1개(엣지는 `parentTypes`로 병합). `parentTypes`는 비어있지 않고 중복 없음.

레퍼런스 픽스처(CONCEPT 예시): folder/document/team 모델을 위 IR로 채운 JSON을 `packages/shared/src/__fixtures__/doc-folder-team.ir.json`에 둔다. 이 픽스처는 `lazyfga-3`·`lazyfga-4`의 골든 테스트 입력이 된다.

## 5. API Design

### 5-1. New / Modified

```ts
// packages/shared/src/model.ts
/** IR 정적 검증. 변환(compiler) 이전에 항상 통과해야 한다. */
export function validateModelIR(ir: ModelIR): ValidationError[]; // 빈 배열 = 유효

export interface ValidationError {
  code: "BAD_NAME" | "DUP_TYPE" | "DUP_RELATION" | "UNKNOWN_PARENT"
      | "UNKNOWN_ROLE" | "UNKNOWN_GROUP" | "EMPTY_GRANT" | "RESERVED_USER"
      | "PARENT_MISSING_PERMISSION" | "DUP_PARENT_RELATION";
  path: string;     // 예: "resources[1].permissions[0].grantedByRoles[2]"
  message: string;
}
```

### 5-2. Error Handling

REST 아님. 발생 가능한 에러 = §4.3 검증 규칙 1~6 위반 → `ValidationError[]`로 수집 반환(예외 던지지 않음, UI가 인라인 표시).

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                              | Estimated | Owner |
|---------|---------------------------------------------------|-----------|-------|
| Phase 1 | `ModelIR` 타입 + 런타임 스키마 정의                | 0.5d      | TBD   |
| Phase 2 | `validateModelIR` 규칙 1~6 구현 + 단위 테스트       | 1d        | TBD   |
| Phase 3 | doc/folder/team 레퍼런스 픽스처 작성               | 0.5d      | TBD   |

### 6-2. Dependencies

- 런타임 검증 스키마: `zod`(또는 동등). 외부 런타임 의존성 최소화(`shared`는 zod만 허용).

## 7. References

- [CONCEPT.md](../CONCEPT.md) — 5 primitive, role×permission 행렬, 비주얼 표현 범위
- [OpenFGA Configuration Language](https://openfga.dev/docs/configuration-language)
- [OpenFGA Modeling: Roles and Permissions](https://openfga.dev/docs/modeling/roles-and-permissions)
