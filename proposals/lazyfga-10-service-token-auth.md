# Service Token & Admin Auth - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M3 cross-review)  |

---

## 1. Summary

두 종류의 접근을 통제한다: (1) **admin** — 환경변수 단일 admin 토큰으로 control-plane(모델·정책·토큰 관리) 전체 접근. (2) **service token** — admin이 발급하는 토큰으로 PDP evaluate 호출 권한. 모든 API는 Bearer 인증 미들웨어를 통과한다.

## 2. Background & Motivation

- 확정된 admin auth = **단일 admin 토큰/계정(MVP)**.
- CONCEPT 신뢰 경계: PDP evaluate(`lazyfga-9`)는 호출자가 임의 subject를 단언하므로, **신뢰된 서비스만** 호출해야 함 → service token 필수. end user 직접 노출 금지.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] admin 토큰(env `ADMIN_TOKEN`) 검증 → `admin` 역할.
- [ ] service token 발급/목록/폐기(admin 전용), 저장은 해시(평문 미저장).
- [ ] Bearer 인증 미들웨어: 토큰 → `{ role: "admin" | "service", tokenId? }`. 라우트별 요구 역할 강제.
- [ ] 역할 매트릭스: control-plane 라우트=admin, `/access/v1/evaluation`=service 또는 admin.

### 3.2 Non-Goals

- [ ] 멀티유저/RBAC, OIDC 로그인(확정상 범위 밖). 후속에 OIDC 콘솔 로그인 추가 가능.
- [ ] service token 스코프 세분화(특정 정책만 허용 등)는 후속. MVP는 "evaluate 전체 허용".

## 4. Technical Design

### 4.1 Architecture Overview

```
요청 → authMiddleware(Bearer)
   ├─ token == ADMIN_TOKEN            → { role:"admin" }
   ├─ sha256(token) ∈ service_token   → { role:"service", tokenId }
   └─ else                            → 401
라우트 가드: requireRole("admin") / requireRole("service","admin")
```

### 4.2 Data Model Changes

신규 테이블 `service_token`:
| column | type | 설명 |
|--------|------|------|
| id | uuid PK | |
| name | text | 발급 라벨(예: "api-gateway") |
| token_hash | text | sha256(평문 토큰). 평문은 발급 응답에서 1회만 노출 |
| created_at | timestamptz | |
| last_used_at | timestamptz null | evaluate 시 갱신(베스트 에포트) |
| revoked_at | timestamptz null | 폐기 시각. non-null이면 거부 |

### 4.3 Core Logic

- 발급: 랜덤 32바이트 토큰 생성 → 평문은 응답으로 1회 반환, DB엔 `sha256` 저장. (평문 재조회 불가)
- 검증: 상수시간 비교로 admin 토큰 우선 매칭, 아니면 `sha256(presented)`로 `service_token` 조회(미폐기). 매칭 토큰의 `last_used_at` 갱신.
- 가드: 각 라우트는 허용 역할 집합을 선언. evaluate는 {service, admin}, 그 외 control-plane은 {admin}.
- 보안: 토큰은 로그에 남기지 않음. 401 응답은 admin/service 구분 정보를 노출하지 않음.

## 5. API Design

### 5-1. New / Modified

```
POST   /tokens        { name } → 201 { id, name, token }   // token 평문 1회 노출
GET    /tokens        → 200 { tokens: [{id,name,createdAt,lastUsedAt,revoked}] }  // 해시/평문 미노출
DELETE /tokens/:id    → 204   // revoke (revoked_at 기록)
```
모두 admin 전용.

```ts
// apps/api/src/middleware/auth.ts
export interface Principal { role: "admin" | "service"; tokenId?: string }
/** Bearer 토큰을 Principal로 해석. 실패 시 401 throw. */
export function authenticate(authorizationHeader: string | undefined): Promise<Principal>;
/** 허용 역할 가드. */
export function requireRole(...roles: Principal["role"][]): Middleware;
```

### 5-2. Error Handling

| Status | Description |
|--------|-------------|
| 401 | 토큰 없음/무효/폐기됨 |
| 403 | 인증됐으나 역할 부족(예: service 토큰으로 control-plane 접근) |
| 404 | 폐기 대상 토큰 없음 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                            | Estimated | Owner |
|---------|-------------------------------------------------|-----------|-------|
| Phase 1 | `service_token` 스키마 + 발급/목록/폐기           | 1d        | TBD   |
| Phase 2 | authMiddleware(admin/service) + requireRole 가드 | 0.5d      | TBD   |
| Phase 3 | 라우트별 역할 적용 + 보안 테스트(401/403)         | 0.5d      | TBD   |

### 6-2. Dependencies

- `drizzle-orm`, Bun/Web Crypto(sha256, 상수시간 비교).
- 환경변수 `ADMIN_TOKEN`(`lazyfga-1` .env).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §6 service token, 신뢰 경계
- `lazyfga-9`(evaluate가 service 역할 요구), `lazyfga-1`(env)
