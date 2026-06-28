# Named Policy Store - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Draft** / In Review / Approved |
| Reviewers  |                                  |

---

## 1. Summary

named policy를 저장·관리한다. 확정된 의미에 따라 **정책 1개 = `(permission, resourceType)` 단일 질문 템플릿**이다. 정책은 사람이 부르기 쉬운 `id`(slug)와, evaluate 시 사용할 OpenFGA relation 매핑을 보유한다.

## 2. Background & Motivation

- 앱이 OpenFGA의 type/relation을 몰라도 `(action, resource)`만 던지면 되게 하려면, "어떤 액션이 어떤 resource 타입의 어떤 relation을 검사하는지"를 등록해두는 레지스트리가 필요하다.
- 단일 질문 템플릿 결정(ROADMAP)에 따라 다중 Check 합성은 없음 → 정책은 가볍고 예측 가능.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] policy CRUD: `id`(slug), `permission`, `resourceType`, `description`.
- [ ] **유일성 불변식:** `(permission, resourceType)` 쌍은 전역 유일(evaluate 조회 키이므로). `id`도 유일.
- [ ] 발행된 현재 모델(`lazyfga-7`)에 `resourceType`와 `can_<permission>` relation이 실제 존재하는지 검증.
- [ ] `lazyfga-9`가 사용할 조회 함수: by id, by `(permission, resourceType)`.

### 3.2 Non-Goals

- [ ] 다중 Check 합성/조건 결합(단일 템플릿 확정). 조건(CEL)은 `lazyfga-14`에서 policy에 선택적 결합.
- [ ] 정책 버전 핀(현재 모델 변경 시 정책 의미 변동 추적)은 후속(메모리 concept-gaps #1 보강 항목으로 기록만).

## 4. Technical Design

### 4.1 Architecture Overview

```
admin → /policies (CRUD) → policy.service → Postgres(policy)
                                 │ 검증
                                 └─ model.service.current() 로 (resourceType, can_<permission>) 존재 확인
lazyfga-9(PDP) → policy.findByActionResource(permission, resourceType)
```

### 4.2 Data Model Changes

신규 테이블 `policy`:
| column | type | 설명 |
|--------|------|------|
| id | text PK | slug. 예: "can-read-document" |
| permission | text | 예: "read" (relation = `can_read`) |
| resource_type | text | 예: "document" |
| description | text null | |
| created_at / updated_at | timestamptz | |
| condition_ref | text null | 예약(`lazyfga-14`) |

제약: `UNIQUE(permission, resource_type)`.

### 4.3 Core Logic

생성/수정 검증:
1. `id`는 slug 규칙(`^[a-z0-9-]+$`), 유일.
2. `(permission, resource_type)` 유일(중복 시 409).
3. 현재 발행 모델 로드 → `resource_type` type 존재 && `can_<permission>` relation 존재 확인. 없으면 422(모델 먼저 발행/수정 안내).
4. evaluate 조회 키는 `(permission, resource_type)`. `id`는 관리·표시·shorthand 호출용.

> 모델이 나중에 바뀌어 `can_<permission>`가 사라지면 정책은 "깨진 상태"가 된다. MVP는 evaluate 시 런타임 에러로 표면화(`lazyfga-9` 참고)하고, 사전 무결성 검사는 발행 시점(`lazyfga-7`)에 경고로만. (정책 버전 핀은 Non-Goal.)

## 5. API Design

### 5-1. New / Modified

```
POST   /policies      { id, permission, resourceType, description? } → 201 { policy }
GET    /policies      → 200 { policies: Policy[] }
GET    /policies/:id  → 200 { policy } | 404
PUT    /policies/:id  { permission?, resourceType?, description? } → 200 { policy }
DELETE /policies/:id  → 204
```
모두 admin 인증(`lazyfga-10`).

```ts
// packages/shared/src/policy.ts
export interface Policy {
  id: string; permission: string; resourceType: string;
  description?: string; conditionRef?: string; // 예약
}
// 서버 내부 조회 계약
export interface PolicyRepo {
  findById(id: string): Promise<Policy | null>;
  findByActionResource(permission: string, resourceType: string): Promise<Policy | null>;
}
```

### 5-2. Error Handling

| Status | Description |
|--------|-------------|
| 401 | 인증 실패 |
| 403 | service 토큰으로 control-plane 접근(역할 부족) |
| 409 | `id` 또는 `(permission, resourceType)` 중복 |
| 422 | slug 규칙 위반 / 현재 모델에 type·relation 부재 |
| 404 | 없는 정책 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                          | Estimated | Owner |
|---------|-----------------------------------------------|-----------|-------|
| Phase 1 | `policy` 스키마 + CRUD + 유일성 제약            | 1d        | TBD   |
| Phase 2 | 현재 모델 대조 검증(type/relation 존재)         | 0.5d      | TBD   |
| Phase 3 | `PolicyRepo` 조회(by id / by action+resource)  | 0.5d      | TBD   |

### 6-2. Dependencies

- `drizzle-orm`, `lazyfga-7`(현재 모델 조회), 인증 미들웨어(`lazyfga-10`).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §3 named policy(질문의 틀)
- 메모리 `lazyfga-concept-logical-gaps` #1(정책=템플릿, object 파라미터)
- `lazyfga-9`(evaluate가 본 저장소 사용)
