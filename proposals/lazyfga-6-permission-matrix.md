# Permission Matrix UI - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Draft** / In Review / Approved |
| Reviewers  |                                  |

---

## 1. Summary

resource 노드를 더블클릭하면 열리는 패널에서, 해당 타입의 role × permission을 **체크박스 행렬**로 편집한다. 행렬 조작이 `Permission.grantedByRoles`/`inheritFromParents`와 `Role` 목록을 변형해 `ModelIR`을 갱신한다(CONCEPT의 "마이크로는 행렬").

## 2. Background & Motivation

- CONCEPT 핵심 단순화: 사용자는 role(부여)만 만들고, permission(검사)은 행렬에서 "어느 role이 이 액션을 주는가"를 체크하면 자동 생성된다(`lazyfga-3`이 union으로 컴파일).
- 행렬은 학습곡선 0의 RBAC식 UI라 비개발자도 다룰 수 있다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] 선택된 ResourceType에 대해: 열=roles, 행=permissions, 셀=`grantedByRoles` 포함 여부 체크박스.
- [ ] role 추가/삭제/이름변경, role별 `assignableBy`(user / 어떤 group) 편집.
- [ ] permission 추가/삭제/이름변경, permission별 `inheritFromParents` 토글(부모 relation별).
- [ ] 편집 즉시 `ModelIR` 갱신 → 캔버스/DSL 미리보기(`lazyfga-5`)와 동기화 + `validateModelIR` 인라인 에러.

### 3.2 Non-Goals

- [ ] 조건(CEL) 부여(= `lazyfga-13/14`).
- [ ] 역할 계층/`implies`(2026-06-28 행렬 방식 확정으로 제외).

## 4. Technical Design

### 4.1 Architecture Overview

```
ResourceType(선택)  ──▶  Matrix Panel
   행: permissions   열: roles   셀: grantedByRoles ∋ role ?
   하단: role 편집(assignableBy), permission 편집(inheritFromParents)
        │ 변형
        ▼
   useModelGraph(zustand ModelIR)  ── 공유 ──▶  캔버스/DSL(lazyfga-5)
```

### 4.2 Data Model Changes

없음(IR 변형, 클라이언트).

### 4.3 Core Logic

행렬 셀 토글(결정적):
- 셀(`permission p`, `role r`) 체크 ON = `p.grantedByRoles`에 `r.name` 추가(중복 없음). OFF = 제거.
- 불변식: `p.grantedByRoles`가 비면 `validateModelIR` 규칙5 위반(EMPTY_GRANT) → 마지막 한 칸 해제 시 경고 표시(허용하되 발행 차단은 `lazyfga-7`).
- role 삭제 시: 해당 role을 참조하던 모든 permission의 `grantedByRoles`에서 제거(고아 금지).
- permission `inheritFromParents` 토글: 같은 ResourceType의 `parents[].relationName` 목록에서 선택(부모가 없으면 토글 비활성).
- role `assignableBy` 편집: `[user]` 기본, group 체크 시 `{kind:"group", group, relation:"member"}` 추가.

표시 규칙: read-only(coverage=false) 모델이면 패널 전체 비활성(`lazyfga-5`와 동일 게이트).

## 5. API Design

### 5-1. New / Modified

```ts
// web/features/permission-matrix/useMatrix.ts
/** 선택된 ResourceType의 행렬 편집 액션. useModelGraph의 IR을 변형. */
export function useMatrix(typeName: string): {
  roles: Role[]; permissions: Permission[]; parents: ParentRef[];
  toggleCell(permission: string, role: string): void;
  addRole(name: string): void; removeRole(name: string): void; renameRole(from: string, to: string): void;
  setRoleAssignableBy(role: string, refs: SubjectRef[]): void;
  addPermission(name: string): void; removePermission(name: string): void;
  toggleInherit(permission: string, parentRelation: string): void;
  errors: ValidationError[]; // 이 타입에 한정
};
```

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| permission grant가 빈 셀 | `EMPTY_GRANT` 경고(인라인), 발행 차단은 `lazyfga-7` |
| role/permission 이름 충돌·예약어 | `validateModelIR` 에러 인라인 |
| read-only 모델 | 패널 비활성 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                            | Estimated | Owner |
|---------|-------------------------------------------------|-----------|-------|
| Phase 1 | 행렬 렌더(roles×permissions) + 셀 토글            | 1d        | TBD   |
| Phase 2 | role/permission CRUD + assignableBy 편집         | 1d        | TBD   |
| Phase 3 | inheritFromParents 토글 + 검증 인라인 표시        | 0.5d      | TBD   |

### 6-2. Dependencies

- `lazyfga-5`(useModelGraph/IR 스토어), `packages/shared`(`Role`,`Permission`,`SubjectRef`,`validateModelIR`).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §1 role×permission 행렬
- `lazyfga-2`(IR), `lazyfga-5`(canvas)
