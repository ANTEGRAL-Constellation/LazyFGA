# Audit Log (DB-backed change tracking) - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-29                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude (M7 cross-review; Codex unavailable) |

---

## 1. Summary

현재 구조화 로그 stub인 `recordAudit`를 **DB 기반 `audit_log`** 로 교체하고, admin `GET /audit` 조회 API와 web 감사 뷰를 추가한다. 컨트롤 플레인 **변경**(모델 발행·정책/토큰/IdP 설정 CRUD·IdP tuple 동기화)과 주요 오류 이벤트를 영속 기록한다. (CONCEPT §6 "audit log: 모델·tuple·정책의 모든 변경을 추적")

## 2. Background & Motivation

- 현재 `apps/api/src/modules/audit/audit.ts`는 `console.log`만 한다(`recordAudit(action, data)`), 재시작하면 사라지고 조회 불가.
- CONCEPT §6: self-hosted control plane은 변경 추적이 필요하다("누가·언제·무엇을 바꿨나").
- 현재 `recordAudit` 호출부는 `model.service`(`model.publish`, `model.publish.db_failure`)와 `pdp.evaluator`(`pdp.evaluate.openfga_error`, `pdp.reason.error`) **둘뿐**이다. 본 명세는 이 sink를 DB로 바꾸고 조회를 연다 — 기존 호출부는 시그니처 호환으로 그대로 두고, `policy`/`auth` 라우트엔 호출을 **추가**하며, `idp.*`는 `lazyfga-15/16`이 구현되며 추가된다(구현 순서상 15/16이 17보다 먼저라 그 시점엔 존재).

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `audit_log` 테이블 + 마이그레이션.
- [ ] `recordAudit`를 DB insert로 교체하되 **절대 호출자를 깨지 않는다**(fire-and-forget + 내부 catch; 감사 실패가 감사 대상 작업을 롤백/실패시키지 않음). 기존 시그니처 호환 + 선택적 `actor`.
- [ ] 컨트롤 플레인 변경에 actor(누가) 기록: admin / `service:<tokenId>` / `idp:<provider>` / `system`.
- [ ] `GET /audit`(admin): 최신순 페이지네이션 + action/actor/기간 필터.
- [ ] web `features/audit`: admin 토큰으로 감사 목록 조회 뷰(explain 패널의 토큰 입력 방식 재사용).

### 3.2 Non-Goals

- [ ] **evaluate 결정의 건별 audit**(hot-path, 대량). 기록 대상은 *변경*(CONCEPT)이며, 평가는 주요 *오류*만 기록한다(기존 `pdp.*` 유지).
- [ ] 보존/회전(retention) 정책, 외부 SIEM 내보내기, 변조 방지 서명 — 후속.
- [ ] 읽기 작업(GET) 감사.
- [ ] audit를 작업과 같은 트랜잭션에 묶기(감사 실패가 작업을 막으면 안 되므로 의도적으로 best-effort post-commit).

## 4. Technical Design

### 4.1 Architecture Overview

```
mutation 핸들러(model/policy/auth/idp) ──recordAudit(action, data, actor)──▶ audit sink
   sink = audit_log INSERT (비차단, 실패 시 console.error만)
admin ──Bearer(admin)──▶ GET /audit?action=&actor=&from=&to=&limit=&cursor= → 최신순 목록
web/features/audit ── admin 토큰 ──▶ GET /audit → 테이블 표시
```

### 4.2 Data Model Changes

신규 테이블. 마이그레이션 번호는 `drizzle-kit generate`가 journal에서 자동 부여한다(현재 HEAD 0002 → 단독 구현 시 **0003**; `lazyfga-15`가 먼저 0003을 추가했으면 0004). 번호를 코드/문서에 하드코딩하지 않는다. 기존 관례(`uuid().defaultRandom()`, `timestamptz`) 준수.

**`audit_log`**

| column | type | 설명 |
|--------|------|------|
| id | uuid PK | |
| occurred_at | timestamptz notNull defaultNow | 인덱스(최신순 조회) |
| actor | text notNull default 'system' | "admin" \| "service:&lt;tokenId&gt;" \| "idp:&lt;provider&gt;" \| "system" |
| action | text notNull | 예: "model.publish", "policy.create", "idp.tuple.write" |
| data | jsonb notNull default '{}' | 작업 상세(versionId, slug, tuple 등) |

> 인덱스: `(occurred_at desc)` 기본; 필요 시 `(action)`·`(actor)` 보조. tuple/모델 원본은 OpenFGA/`model_version`에 있고 audit는 "변경 사실"만 가진다(데이터 중복 최소, ARCHITECTURE 데이터 소유 원칙). 주의: `occurred_at`은 삽입 시각(이벤트 시각 근사, best-effort), 동일 시각은 `id`(uuid)로 안정 정렬하므로 삽입 순서와 다를 수 있다.

### 4.3 Core Logic

**`recordAudit` 교체(비차단).**
```ts
export function recordAudit(action: string, data?: Record<string, unknown>, actor?: string): void {
  // fire-and-forget: 감사 실패가 감사 대상 작업을 절대 깨지 않는다.
  db.insert(auditLog).values({ action, data: data ?? {}, actor: actor ?? "system" })
    .catch((e) => console.error(`[audit] insert failed: ${action}`, e));
}
```
- 시그니처 **하위호환**: 기존 `recordAudit(action, data)` 호출부는 그대로 동작(actor 기본 "system"). 컨트롤 플레인 라우트는 principal에서 actor를 채워 호출하도록 점진 갱신(예: `recordAudit("policy.create", {id}, principalActor(c))`).
- `principalActor(principal)`: `admin` → "admin", `service` → `tokenId ? "service:"+tokenId : "service"`(‘service:undefined’ 방지). IdP 경로는 `"idp:" + provider`.
- post-commit 호출 유지(예: `model.service`는 트랜잭션 커밋 후 호출 — 변경 없음). 비동기 insert라 hot-path 지연 없음.

**감사 대상 action(변경 중심).**
- model: `model.publish`, `model.publish.db_failure`(기존).
- policy: `policy.create`, `policy.update`, `policy.delete`(`lazyfga-8` 라우트에 호출 추가).
- auth: `token.create`, `token.revoke`(`lazyfga-10` 라우트에 호출 추가).
- idp: `idp.connection.create/update/delete`, `idp.rule.*`, `idp.tuple.write/delete/error`, `idp.webhook.unauthorized`(`lazyfga-15/16`).
- pdp(오류만): `pdp.evaluate.openfga_error`, `pdp.reason.error`(기존). 정상 결정은 기록하지 않음.

**`GET /audit` 조회.** 최신순(`occurred_at desc, id desc`) keyset 페이지네이션. 필터: `action`(정확/접두), `actor`, `from`/`to`(시각). admin 전용.

### 4.4 Web (`features/audit`)

admin 토큰 입력(explain 패널과 동일 패턴) → `GET /audit` → 시간·actor·action·data 요약 테이블. 필터 입력. 읽기 전용. 단 `GET /audit`는 admin 전용이므로 **admin 토큰**이 필요하다(explain에서 쓰는 service 토큰으로는 403 → "admin token required"로 안내).

## 5. API Design

### 5-1. New / Modified

```
GET /audit?action=&actor=&from=&to=&limit=&cursor=      (admin)
→ 200 { entries: AuditEntry[]; nextCursor?: string }
```

```ts
// packages/shared/src/audit.ts — web가 GET /audit를 소비하므로 shared에 두고 index에 `export * from "./audit"` 등록
export interface AuditEntry {
  id: string;
  occurredAt: string;     // ISO
  actor: string;
  action: string;
  data: Record<string, unknown>;
}

// apps/api/src/modules/audit/audit.ts
export function recordAudit(action: string, data?: Record<string, unknown>, actor?: string): void;
```

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| audit insert 실패 | `console.error`만, 호출자 영향 없음(비차단) |
| GET /audit 비admin | 401/403(`requireRole("admin")`) |
| 잘못된 필터/cursor | 400 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                          | Estimated | Owner |
|---------|---------------------------------------------------------------|-----------|-------|
| Phase 1 | `audit_log` 테이블/마이그레이션 + `recordAudit` DB 교체(비차단) + 단위 테스트 | 1d        | TBD   |
| Phase 2 | mutation 라우트에 actor 채워 호출 갱신 + `GET /audit`(필터·keyset) | 1d        | TBD   |
| Phase 3 | web `features/audit` 뷰 + E2E                                   | 0.5d      | TBD   |

### 6-2. Dependencies

- `db`/Drizzle, 인증 미들웨어(`lazyfga-10`; GET admin), `recordAudit` 호출부(`model`·`pdp` 기존; `policy`·`auth` 추가; `idp` 는 `lazyfga-15/16`).
- 조건부 의존: `idp.*` audit과 마이그레이션 0004는 `lazyfga-15`(0003)가 17보다 먼저 구현될 때 성립(권장 구현 순서가 그러함). 마이그레이션 번호는 generate 시점 journal로 확정.

## 7. References

- [CONCEPT.md](../CONCEPT.md) §6 self-hosted control plane(audit log)
- [ARCHITECTURE.md](../ARCHITECTURE.md) `api/modules/audit`, 데이터 소유(audit는 lazyFGA Postgres)
- `lazyfga-15/16`(idp.* audit), `lazyfga-8`(policy), `lazyfga-10`(token·auth)
