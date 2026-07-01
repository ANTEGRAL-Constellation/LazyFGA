# IdP Webhook Core (receiver + adapter interface + mapping engine) - Spec Proposal

| Item      | Detail                                                              |
| --------- | ------------------------------------------------------------------- |
| Author    | Seonguk Moon                                                        |
| Created   | 2026-06-29                                                          |
| Status    | **Implemented**                                                     |
| Reviewers | Claude (M6 cross-review + adversarial re-review; Codex unavailable) |

---

## 1. Summary

IdP 이벤트(사용자 생성·역할 부여 등)를 webhook으로 받아 OpenFGA tuple로 동기화하는 **provider-agnostic 코어**다. 서명 검증된 webhook 수신 → adapter가 provider payload를 정규 `IdpEvent`로 정규화 → **설정 가능한 매핑 규칙**(Q3=B)으로 tuple write/delete를 적용한다. provider별 파싱·서명 방식은 adapter가 구현하고, ZITADEL adapter는 `lazyfga-16`이 채운다. (CONCEPT §5 mode ① 신원 동기화)

## 2. Background & Motivation

- CONCEPT 차별: _"Bring your IdP."_ 인증은 IdP, 인가는 lazyFGA. 어느 IdP든 공통분모는 **"OIDC claims + webhook"** 하나다(§5).
- **시점이 다른 두 모드**(§5): ① 신원 동기화(인증 플로우 시점, 이벤트→tuple) — 본 명세. ② 결정(요청 시점) — 기존 PDP(`lazyfga-9`).
- 현재 lazyFGA에 **tuple 기록 경로가 없다**(`modules/tuples` 미구현). 본 코어가 첫 tuple 기록 경로다. 단 실제 tuple 흐름은 `lazyfga-16`이 ZITADEL adapter를 등록해야 시작되며, 그때부터 playground/데모 데이터의 본류가 된다(Q4=A).
- Q3=B 결정: 이벤트→tuple 매핑을 코드 고정이 아니라 **설정(Postgres + admin API)** 으로 둔다 → 동일 코어로 여러 IdP를 덮는 IdP-agnostic을 증명한다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `POST /idp/webhook/:provider`: 서명 검증 → `adapter.parseEvents` → 매핑 엔진 → OpenFGA write/delete → audit.
- [ ] provider별 `IdpAdapter` 인터페이스: `verifySignature` + `parseEvents`(raw payload → 정규 `IdpEvent[]`).
- [ ] 정규 이벤트 계약 `IdpEvent`(provider 독립).
- [ ] 설정 저장소 + admin CRUD: `idp_connection`(provider·signingSecret·enabled), `idp_mapping_rule`(eventType·match·tupleTemplate·op).
- [ ] 매핑 엔진: 정규 이벤트 → 규칙 매칭 → **안전한 템플릿 렌더**(placeholder 치환만) → tuple. **멱등 적용**(write 이미 존재 / delete 없음 = no-op).
- [ ] tuple write/delete는 기존 `gateway.write`를 **tuple 1개씩 개별 호출**한다(`gateway.write({writes:[t]})` 또는 `{deletes:[t]}`). 게이트웨이 변경 없음. (SDK 기본 transaction 모드에서 배치 Write는 원자적이라 한 tuple 오류가 전체를 롤백하고 per-tuple 멱등/카운트가 불가하므로 개별 호출로 격리.)

### 3.2 Non-Goals

- [ ] ZITADEL-specific 파싱·서명 방식·기본 규칙 시드 — `lazyfga-16`.
- [ ] mode ② 결정 호출 — 기존 PDP(`lazyfga-9`).
- [ ] 아웃바운드(lazyFGA → IdP) 동기화.
- [ ] 임의 코드/표현식 매핑. 템플릿은 `{{path}}` 치환 + 동등 비교 매칭만(코드 실행 없음).
- [ ] 매핑 관리 web UI(MVP는 admin REST API. UI는 `lazyfga-16` 또는 여건).
- [ ] OIDC claims를 contextual tuple로 쓰는 결정-시점 주입(이는 평가 경로이며 본 명세는 영속 tuple 동기화).

## 4. Technical Design

### 4.1 Architecture Overview

```
IdP ──(HTTP POST, signed)──▶ POST /idp/webhook/:provider        (서명 인증, admin/service 토큰 아님)
   → conn = idp_connection by provider  (연결 없음 404 / disabled 403)
   → adapter = registry[provider]       (등록 adapter 없음 501 — lazyfga-15는 실제 adapter 미포함; lazyfga-16이 zitadel 등록)
   → adapter.verifySignature(rawBody, headers, conn.signingSecret)   (실패 401)
   → events: IdpEvent[] = adapter.parseEvents(body, headers)
   → for each event:
        rules = mapping rules(conn, event.type) 중 match 충족
        for each rule (priority 순): tuple = render(rule.tupleTemplate, event)
                                     gateway.write({writes:[tuple]}) | ({deletes:[tuple]})  [개별·멱등] → audit
   → 200 { applied, skipped, failed }

admin ──Bearer(admin)──▶ /idp/connections , /idp/connections/:id/rules   (CRUD)
```

### 4.2 Data Model Changes

신규 테이블(마이그레이션 0003). 기존 스키마 관례(`uuid().defaultRandom()`, `timestamptz`, `unique()` 콜백) 준수.

**`idp_connection`**

| column                  | type                         | 설명                                                      |
| ----------------------- | ---------------------------- | --------------------------------------------------------- |
| id                      | uuid PK                      |                                                           |
| provider                | text notNull                 | adapter 키. 예: "zitadel". `UNIQUE`                       |
| signing_secret          | text notNull                 | HMAC 검증용 raw secret. **write-only**(GET 응답에 미노출) |
| enabled                 | boolean notNull default true |                                                           |
| created_at / updated_at | timestamptz                  |                                                           |

**`idp_mapping_rule`**

| column                  | type                              | 설명                                     |
| ----------------------- | --------------------------------- | ---------------------------------------- |
| id                      | uuid PK                           |                                          |
| connection_id           | uuid notNull FK→idp_connection.id |                                          |
| event_type              | text notNull                      | 정규 이벤트 타입. 예: "user.grant.added" |
| match                   | jsonb notNull default '[]'        | 동등 비교 술어 배열(아래)                |
| tuple_template          | jsonb notNull                     | `{ user, relation, object }` 템플릿      |
| op                      | text notNull                      | "write" \| "delete"                      |
| priority                | integer notNull default 0         | 적용 순서(작을수록 먼저)                 |
| created_at / updated_at | timestamptz                       |                                          |

> tuple은 평문(조건 없음). 조건은 모델 레벨에서 강제된다(`lazyfga-14`). `gateway.write`는 변경 없음.

### 4.3 Core Logic

**웹훅 인증 경계(중요).** `/idp/webhook/*`는 admin/service 토큰 미들웨어를 **거치지 않고**, adapter 서명 검증으로만 인증한다. HMAC은 **raw body 바이트**에 대해 계산하므로 JSON 파싱 전에 raw body를 확보한다(Hono `c.req.arrayBuffer()`; 그 바이트에서 JSON을 파싱하면 재래핑을 피함). 비교는 상수시간. 미등록 provider/연결 없음 → 404, 연결은 있으나 adapter 미등록 → 501, disabled → 403, 서명 불일치 → 401.

**라우터 구성(가드 분리).** index.ts에 전역 auth 미들웨어는 없고 각 라우터가 self-guard한다(기존 모듈은 `router.use("*", requireRole(...))` 패턴). 따라서 웹훅은 **가드 없는 별도 라우터**로 마운트하고, `requireRole("admin")`은 `/idp/connections*`·`/idp/rules*`에만 건다(웹훅을 같은 라우터의 `use("*")` 아래 두면 안 됨).

**정규 이벤트 `IdpEvent`.**

```ts
interface IdpEvent {
  type: string; // 정규 타입. 예: "user.grant.added"
  subject: { id: string }; // 영향받는 user 식별자(OpenFGA user id로 쓰임)
  attributes: Record<string, string>; // 정규화된 평탄 필드. 예: { projectRole:"editor", orgId:"123" }
}
```

**Adapter 인터페이스.**

```ts
interface IdpAdapter {
  provider: string;
  verifySignature(rawBody: Uint8Array, headers: Headers, secret: string): boolean;
  parseEvents(body: unknown, headers: Headers): IdpEvent[]; // provider payload → 정규 이벤트들
}
```

provider별 adapter를 레지스트리(`Record<string, IdpAdapter>`)에 등록. `lazyfga-16`이 `zitadel`을 등록한다.

**매핑 규칙.**

- `match`: `Array<{ field: string; equals: string }>` — `field`는 이벤트로의 경로(`type` | `subject.id` | `attributes.<k>`), 모두 동등하면 매칭(빈 배열 = 항상 매칭). `event.type === rule.event_type`는 1차 필터.
- `tuple_template`: `{ user: string; relation: string; object: string }`. 각 문자열은 `{{path}}` placeholder를 이벤트 값으로 치환(`{{subject.id}}`, `{{attributes.projectRole}}`). 미해결 placeholder가 남으면 그 규칙은 실패 처리(skip + audit).
- **주입 방지(중요):** `user`/`object`는 템플릿에 **리터럴 `type:` 접두를 강제**하고 placeholder는 id 부분에만 허용한다(`document:{{attributes.resourceId}}` ✅, `{{attributes.obj}}` ❌). 치환되는 값에 `:`·`#`·`*`·공백이 있으면 거부한다(임의 타입·userset·와일드카드 주입 차단).
- 렌더 결과 검증: `user`/`object`는 `type:id` 형식, `relation`은 식별자. 위반 시 실패 처리.

**개별·멱등 적용.** 각 tuple은 `gateway.write`로 **하나씩** 적용한다(배치는 SDK 기본 transaction 모드에서 원자적이라 한 tuple 실패가 전체를 롤백 → per-tuple 멱등/카운트 불가). 멱등 처리: `op:"write"`가 "이미 존재", `op:"delete"`가 "없음" 오류를 내면 no-op 성공으로 간주(웹훅 재전송 안전). 단 OpenFGA는 두 경우 모두 `write_failed_due_to_invalid_input` 코드로 표면화하므로, **정확히 (해당 코드 + 현재 op + 메시지 패턴) 일치할 때만** 멱등 흡수하고 그 밖의 invalid-input은 결정적 실패로 센다.

**실패 격리 / IdP 재시도.**

- **매칭 규칙 없음**: 의도된 무시 → `skipped`로 카운트, audit `idp.tuple.skip`(오류 아님).
- **결정적 오류**(미해결 placeholder, 무효 tuple 형식, **OpenFGA 4xx validation** — `type_not_found`·`relation_not_found`·`invalid_tuple` 등): 해당 규칙만 `failed`로 세고 audit(`idp.tuple.error`), 나머지는 계속. 웹훅은 **200** + `{applied, skipped, failed}`(재시도해도 안 고쳐지는 오류로 무한 재시도 금지).
- **일시적 오류만**(OpenFGA 5xx/타임아웃/네트워크 = `FgaApiInternalError` 등): **502**로 응답해 IdP가 재전송하게 한다. OpenFGA 4xx를 502로 보내면 무한 재시도가 되므로 명확히 구분한다.

**audit.** 각 tuple op를 `recordAudit("idp.tuple.write"|"idp.tuple.delete"|"idp.tuple.error"|"idp.tuple.skip", {...})`로 기록(`lazyfga-17`에서 DB). **서명 실패(미인증)는 DB audit이 아닌 앱 로그(`console.warn`)로만** 남긴다 — 미인증 요청이 audit_log를 무한 적재하는 amplification을 막기 위함(adversarial 리뷰 반영).

### 4.4 Security

- **signing secret**: `idp_connection.signing_secret`에 raw 저장(HMAC 검증에 원문 필요). GET 응답에서 절대 반환하지 않음(write-only). self-hosted 신뢰 경계 안. 회전 권장, 저장 암호화는 배포 관심사로 문서화.
- **재전송(replay)**: 서명 + (adapter별) timestamp/nonce 검사(`lazyfga-16`). MVP는 멱등 적용으로 재전송 피해를 무해화.
- **주체 주입 방지**: 웹훅은 서명으로 신뢰하되, **설정된 규칙만** tuple을 쓸 수 있다. 구체적으로 치환 값에서 `:`·`#`·`*`·공백을 금지하고 type 접두는 템플릿 리터럴로 고정한다(§4.3) → 이벤트 필드가 임의 타입·userset·와일드카드를 주입할 수 없다.

## 5. API Design

### 5-1. New / Modified

```
# 웹훅(서명 인증, 토큰 불요)
POST /idp/webhook/:provider          → 200 { applied, skipped, failed } | 401 | 403 | 404 | 502

# 설정 CRUD(admin 인증)
POST   /idp/connections              { provider, signingSecret, enabled? }      → 201 { connection }   # secret 미반향
GET    /idp/connections              → 200 { connections }                                            # secret 제외
PUT    /idp/connections/:id          { signingSecret?, enabled? }               → 200 { connection }
DELETE /idp/connections/:id          → 204                                                            # 규칙 cascade
POST   /idp/connections/:id/rules    { eventType, match, tupleTemplate, op, priority? } → 201 { rule }
GET    /idp/connections/:id/rules    → 200 { rules }
PUT    /idp/rules/:ruleId            { ... }                                     → 200 { rule }
DELETE /idp/rules/:ruleId            → 204
```

```ts
// apps/api/src/modules/idp/types.ts  (서버측 계약)
export interface IdpEvent {
  type: string;
  subject: { id: string };
  attributes: Record<string, string>;
}
export interface IdpAdapter {
  provider: string;
  verifySignature(rawBody: Uint8Array, headers: Headers, secret: string): boolean;
  parseEvents(body: unknown, headers: Headers): IdpEvent[];
}
export interface MappingRule {
  eventType: string;
  match: Array<{ field: string; equals: string }>;
  tupleTemplate: { user: string; relation: string; object: string };
  op: "write" | "delete";
  priority: number;
}
/** 이벤트에 규칙을 적용해 tuple write/delete 수행. 멱등. */
export function applyEvents(
  events: IdpEvent[],
  rules: MappingRule[],
): Promise<{ applied: number; skipped: number; failed: number }>;
```

### 5-2. Error Handling

| Status         | 상황                                                                           |
| -------------- | ------------------------------------------------------------------------------ |
| 200            | 처리 완료(매칭 없음/결정적 skip 포함) — `{applied, skipped, failed}`           |
| 401            | 서명 불일치/누락                                                               |
| 403            | 연결 disabled                                                                  |
| 404            | 미등록 provider / 연결 없음                                                    |
| 501            | 연결은 있으나 provider adapter 미등록(lazyfga-16 전)                           |
| 502            | OpenFGA 일시(5xx/네트워크) 오류 — IdP 재전송 유도(4xx validation은 200+failed) |
| 400/409 (CRUD) | 잘못된 설정 / provider 중복                                                    |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                       | Estimated | Owner |
| ------- | ------------------------------------------------------------------------------------------ | --------- | ----- |
| Phase 1 | 테이블 + 마이그레이션 0003 + repo + `IdpEvent`/`IdpAdapter`/`MappingRule` 계약             | 1d        | TBD   |
| Phase 2 | connection/rule CRUD(admin) + secret write-only 처리                                       | 1d        | TBD   |
| Phase 3 | 웹훅 엔드포인트(서명 dispatch) + 매핑 엔진(템플릿·멱등 적용) + audit + fake adapter 테스트 | 1.5d      | TBD   |

### 6-2. Dependencies

- `apps/api/src/openfga`(`gateway.write` 기존), `db`/Drizzle, 인증 미들웨어(`lazyfga-10`; CRUD admin), audit(stub → `lazyfga-17`).
- `lazyfga-16`: `zitadel` adapter가 `IdpAdapter`를 구현하고 데모용 기본 규칙을 시드.
- HMAC: Node/Bun `crypto`(추가 의존성 없음).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §5 IdP-agnostic 연동(mode ① 신원 동기화)
- [ARCHITECTURE.md](../ARCHITECTURE.md) `api/modules/idp`(idp.routes + adapters)
- [OpenFGA Write API](https://openfga.dev/docs/interacting/managing-relationships-between-objects) · [Token claims → contextual tuples](https://openfga.dev/docs/modeling/token-claims-contextual-tuples)
- `lazyfga-16`(ZITADEL adapter), `lazyfga-9`(mode ② 결정), `lazyfga-17`(audit DB 전환)
