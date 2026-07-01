# ZITADEL Adapter (flagship: event to tuple) - Spec Proposal

| Item      | Detail                                                              |
| --------- | ------------------------------------------------------------------- |
| Author    | Seonguk Moon                                                        |
| Created   | 2026-06-29                                                          |
| Status    | **Implemented**                                                     |
| Reviewers | Claude (M6 cross-review + adversarial re-review; Codex unavailable) |

---

## 1. Summary

`lazyfga-15`의 `IdpAdapter` 인터페이스를 **ZITADEL**에 대해 구현한다: ZITADEL Actions V2(외부 webhook)가 보내는 서명된 요청을 검증하고, 사용자/권한부여(grant) 이벤트를 정규 `IdpEvent`로 정규화한다. 데모용 기본 매핑 규칙(ZITADEL 프로젝트 역할 → OpenFGA tuple)을 시드한다. lazyFGA의 "Bring your IdP"를 증명하는 첫 레시피다(CONCEPT §5).

## 2. Background & Motivation

- CONCEPT §5: ZITADEL을 **첫 레시피**로 삼되 같은 패턴이 Keycloak·Auth0에 매핑된다. 출품 우선순위에서 M6(ZITADEL)은 "강력 권장".
- ZITADEL **Actions V2**는 외부 webhook을 호출하고 **HMAC 서명**으로 무결성을 보장한다(`ZITADEL-Signature` 헤더 = 콘텐츠+타임스탬프 기반 HMAC; Target마다 Signing Key 발급). → `lazyfga-15`의 서명 검증 + 매핑 엔진에 그대로 맞는다.
- 원 컨셉: ZITADEL의 프로젝트 단위 user grant를 직관적인 OpenFGA 그룹/역할 tuple로 재구성한다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `zitadelAdapter: IdpAdapter`(`provider:"zitadel"`)를 레지스트리에 등록.
- [ ] `verifySignature`: `ZITADEL-Signature` 헤더 + raw body + connection signing key로 HMAC-SHA256 검증(타임스탬프 허용오차로 replay 차단, 상수시간). **HMAC 바이트 구성**은 ZITADEL `actions.ValidateRequestPayload`와 동일하게 맞추고, 타임스탬프(replay) 검사는 그 위에 추가한다.
- [ ] `parseEvents`: ZITADEL Actions V2 이벤트 payload → 정규 `IdpEvent[]`. grant 이벤트는 **grant당 1 이벤트**(attributes: `projectId`·`grantId`)로 정규화한다(데모 seed는 projectId 기반 → 추가/삭제 대칭·삭제 신뢰 가능). 역할(roleKey) 단위 매핑은 선택 확장(§4.3).
- [ ] 데모 기본 매핑 규칙 시드(`scripts/`): **데모 모델(team#member 포함) 발행 후** grant added/changed/removed → OpenFGA tuple write/delete 규칙 삽입(편집 가능, Q3=B).
- [ ] ZITADEL 연동 설정 가이드: Target(webhook) 생성 + Execution(이벤트 바인딩) 절차 문서.

### 3.2 Non-Goals

- [ ] webhook 수신·서명 검증 프레임·매핑 엔진 자체 — `lazyfga-15`.
- [ ] 데모 set 밖의 모든 ZITADEL 이벤트 망라(MVP는 user 생성 + grant 추가/변경/삭제).
- [ ] 역할(roleKey) 단위 grant 생명주기의 완전 동기화. ZITADEL grant **삭제 이벤트가 roleKey를 나열하지 않을 수 있어** 역할 단위 tuple의 정확한 삭제가 보장되지 않는다(stale tuple 가능). 데모 seed는 projectId 기반으로 이 한계를 회피한다.
- [ ] lazyFGA → ZITADEL 아웃바운드, ZITADEL API 호출(수신만).
- [ ] mode ② 결정(PDP, `lazyfga-9`).

## 4. Technical Design

### 4.1 Architecture Overview

```
[설정] admin이 ZITADEL에 Target(webhook → POST /idp/webhook/zitadel, Signing Key) 생성
       + Execution으로 이벤트(user.human.added, user.grant.added/removed)를 Target에 바인딩
       lazyFGA: idp_connection(provider="zitadel", signing_secret=<Signing Key>) + 시드 규칙

[런타임] ZITADEL ──(서명 POST)──▶ /idp/webhook/zitadel
          → lazyfga-15 코어 → zitadelAdapter.verifySignature(rawBody, headers, secret)
          → zitadelAdapter.parseEvents(body) → IdpEvent[]  (grant는 roleKey당 1개)
          → 매핑 규칙 적용 → gateway.write/delete(tuple) → audit
```

### 4.2 Data Model Changes

신규 테이블 없음(`lazyfga-15`의 `idp_connection`/`idp_mapping_rule` 사용). ZITADEL 연결은 `provider="zitadel"` 행 1개 + 규칙들. 시드는 `scripts/`의 idempotent 스크립트(있으면 갱신, 없으면 삽입).

### 4.3 Core Logic

**`verifySignature`(ZITADEL Actions V2).**

- 헤더 `ZITADEL-Signature`에서 타임스탬프 `t`와 HMAC `v`를 파싱.
- `now - t`가 허용오차(기본 5분) 초과면 거부(replay 방지).
- `HMAC_SHA256(signing_key, <ZITADEL 규약의 서명 페이로드: t + raw body>)`를 계산해 `v`와 **상수시간** 비교. ZITADEL 공식 `actions.ValidateRequestPayload`와 동일한 바이트 구성을 따른다(정확한 구성은 ZITADEL 문서/SDK로 확정).
- 키는 `idp_connection.signing_secret`(Target Signing Key).

**`parseEvents`(payload → 정규 이벤트).** ZITADEL Actions V2 이벤트 트리거 payload에서 aggregate(user) id와 이벤트 타입, 관련 필드를 추출한다. 정확한 payload JSON 스키마는 이벤트 트리거별로 다르므로 구현 시 샘플 payload로 확정한다.

- `user.human.added` → `IdpEvent{ type:"user.human.added", subject:{id:<userId>}, attributes:{ orgId, username? } }`.
- `user.grant.added` / `user.grant.changed` / `user.grant.removed` → grant당 `IdpEvent{ type, subject:{id:<userId>}, attributes:{ projectId, grantId } }`. **projectId/grantId는 추가·변경·삭제 이벤트 모두에 존재**하므로 삭제가 roleKey 의존 없이 신뢰성 있게 대칭된다.
- **역할 단위(선택 확장):** roleKey별 tuple이 필요하면 `parseEvents`가 roleKey당 이벤트로 펼치고 `attributes.projectRole=<정규화 roleKey>`를 넣는다. roleKey는 자유 문자열이라 `:`/`#`/`*`/공백을 `_`로 치환해 식별자-안전 slug로 만든 뒤 사용하고(원문은 `attributes.projectRoleRaw`), `lazyfga-15` 주입 방지 제약을 통과시킨다. 단 grant 삭제 이벤트가 roleKey를 안 주면 역할 단위 삭제는 stale 위험(Non-Goal) — 그래서 데모 seed는 이 경로를 쓰지 않는다.

> 정확한 ZITADEL 이벤트명/페이로드 필드는 ZITADEL 이벤트 목록과 샘플로 확정한다(문서상 `user.human.added` 확인됨; grant 계열 이벤트명은 구현 시 검증).

**데모 기본 매핑 규칙(시드, 편집 가능).** "프로젝트 grant → OpenFGA 그룹(team) 멤버십"으로 재구성(원 컨셉의 '직관적 그룹'). **삭제 신뢰성을 위해 projectId로 키잉**한다:

- `user.grant.added` / `user.grant.changed`, match `[]` → `op:"write"`, template `{ user:"user:{{subject.id}}", relation:"member", object:"team:{{attributes.projectId}}" }`(변경도 멱등 write로 멤버십 보장).
- `user.grant.removed`, match `[]` → `op:"delete"`, 동일 template.
- (선택) `user.human.added` → no-op(향후 user 등록 규칙 여지).

> `projectId`는 ZITADEL 생성 식별자(숫자)라 `lazyfga-15` 주입 방지 제약(type 접두 리터럴, 치환값에 `:`/`#`/`*`/공백 금지)을 만족한다. Q3=B에 따라 admin이 `idp_mapping_rule` API로 자유롭게 바꾼다(예: 역할 단위 `team:{{attributes.projectRole}}` — 단 위의 역할 단위 삭제 한계 참고). **모델 발행은 `lazyfga-19` 데모 오케스트레이터가 담당**하며(team#member 필요), 본 시드(`scripts/seed-zitadel-rules.ts`)는 연결+규칙만 넣는다. team#member 모델이 미발행이면 grant write가 `type_not_found`로 결정적 실패하므로, 시드 단독 실행 전 모델이 발행돼 있어야 한다.

### 4.4 ZITADEL 연동 절차(문서)

1. ZITADEL에 **Target** 생성(REST webhook = `https://<lazyfga>/idp/webhook/zitadel`) → Signing Key 발급.
2. `PUT /v2/actions/executions`로 이벤트(`user.human.added`, grant 계열)를 Target에 바인딩.
3. lazyFGA: `POST /idp/connections { provider:"zitadel", signingSecret:<Signing Key> }` + 시드 규칙 스크립트 실행.

## 5. API Design

### 5-1. New / Modified

신규 REST 없음(`lazyfga-15`의 `/idp/webhook/:provider`·CRUD 재사용). 신규 코드:

```ts
// apps/api/src/modules/idp/adapters/zitadel.ts
import type { IdpAdapter, IdpEvent } from "../types";

export const zitadelAdapter: IdpAdapter = {
  provider: "zitadel",
  verifySignature(rawBody, headers, secret) {
    /* ZITADEL-Signature(t,v) HMAC-SHA256, replay 허용오차 */
  },
  parseEvents(body, headers): IdpEvent[] {
    /* user.human.added / user.grant.* → roleKey당 1 이벤트 */
  },
};
// 레지스트리 등록: registry["zitadel"] = zitadelAdapter
```

`scripts/seed-zitadel-rules.ts`(또는 데모 시나리오 스크립트): 위 기본 규칙을 idempotent 삽입.

### 5-2. Error Handling

| 상황                                       | 처리                                                                             |
| ------------------------------------------ | -------------------------------------------------------------------------------- |
| `ZITADEL-Signature` 누락/형식 오류         | `lazyfga-15`에서 401                                                             |
| 타임스탬프 허용오차 초과(replay)           | 401                                                                              |
| HMAC 불일치                                | 401                                                                              |
| 알 수 없는 이벤트 타입 / payload 필드 부재 | `parseEvents`가 빈 배열 반환 → 200 no-op + `idp.webhook.no_events` audit(흔적만) |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                      | Estimated | Owner |
| ------- | ----------------------------------------------------------------------------------------- | --------- | ----- |
| Phase 1 | `zitadelAdapter.verifySignature`(HMAC + replay) + 단위 테스트(고정 payload/서명)          | 1d        | TBD   |
| Phase 2 | `parseEvents`(user/grant, roleKey 펼침) + 레지스트리 등록 + 테스트                        | 1d        | TBD   |
| Phase 3 | 데모 기본 규칙 시드 스크립트 + ZITADEL 연동 절차 문서 + E2E(서명된 grant payload → tuple) | 1d        | TBD   |

### 6-2. Dependencies

- `lazyfga-15`(webhook 코어·`IdpAdapter` 인터페이스(`apps/api/src/modules/idp/types.ts`)·매핑 엔진·registry).
- `lazyfga-7`(모델 발행): 데모 시나리오 스크립트가 규칙 시드 **이전에** team#member를 가진 데모 모델(예: 기존 `docFolderTeamIR` 픽스처)을 발행한다.
- ZITADEL Actions V2(Target/Execution) — self-host ZITADEL 또는 데모 인스턴스. HMAC: Bun/Node `crypto`.
- 참고 구현: ZITADEL `actions.ValidateRequestPayload`(서명 바이트 구성).

## 7. References

- [ZITADEL Actions V2 usage (Content Signing, ZITADEL-Signature)](https://zitadel.com/docs/guides/integrate/actions/usage)
- [Testing request signature (ValidateRequestPayload 참고 구현)](https://zitadel.com/docs/guides/integrate/actions/testing-request-signature)
- [Testing event execution (executions, user.human.added)](https://zitadel.com/docs/guides/integrate/actions/testing-event)
- [Actions V1 → V2 (외부 webhook + HMAC)](https://zitadel.com/docs/guides/integrate/actions/migrate-from-v1)
- `lazyfga-15`(IdP webhook core), CONCEPT §5
