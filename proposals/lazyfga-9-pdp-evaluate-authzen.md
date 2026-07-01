# PDP Evaluate (AuthZEN 1.0 호환) - Spec Proposal

| Item      | Detail                          |
| --------- | ------------------------------- |
| Author    | Seonguk Moon                    |
| Created   | 2026-06-28                      |
| Status    | **Implemented**                 |
| Reviewers | Claude, Codex (M3 cross-review) |

---

## 1. Summary

OpenID AuthZEN 1.0 Access Evaluation 형태의 PDP 엔드포인트를 제공한다. 요청의 `(action, resource.type)`로 named policy(`lazyfga-8`)를 찾아, 단일 OpenFGA `Check`로 allow/deny를 결정해 AuthZEN 응답으로 반환한다. 이것이 "REST 한 줄로 꽂는 PDP"의 실체다.

## 2. Background & Motivation

- CONCEPT/메모리 결정: PDP는 **AuthZEN 1.0 호환** → AuthZEN을 아는 PEP/게이트웨이와 즉시 호환. named policy = 단일 질문 템플릿(Check 1회).
- 앱은 type/relation을 몰라도 `(subject, action, resource)`만 던지면 된다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `POST /access/v1/evaluation` (AuthZEN): `{subject, action, resource, context?}` → `{decision}`.
- [ ] `(action.name, resource.type)`로 정책 조회 → `Check(user, can_<permission>, <resource.type>:<resource.id>, context)`.
- [ ] service token 인증 필수(`lazyfga-10`). end user 직접 호출 금지.
- [ ] 정책 미존재 → 안전하게 deny(200, 정보 누출 최소). 모델 깨짐(relation/type 부재) → 500(fail-closed 아님, `lazyfga-8` 무결성 이슈로 표면화).

### 3.2 Non-Goals

- [ ] `reason`(사람이 읽는 이유)은 본 명세 범위 밖 → `lazyfga-11`(M4)에서 응답 `context`에 추가.
- [ ] 다중 Check 합성(단일 템플릿 확정).
- [ ] 결정 캐싱·consistency 토큰 튜닝(MVP는 OpenFGA 기본 consistency 사용; 후속 최적화).
- [ ] 조건(CEL) 평가는 `lazyfga-14`까지 `context`를 통과만 시킴(정책에 condition 없으면 무시).

## 4. Technical Design

### 4.1 Architecture Overview

```
PEP ──Bearer(service token)──▶ POST /access/v1/evaluation
   → auth middleware(lazyfga-10)
   → pdp.evaluate:
       policy = PolicyRepo.findByActionResource(action.name, resource.type)
       user   = `${subject.type}:${subject.id}`
       object = `${resource.type}:${resource.id}`
       allowed = openfga.check(user, `can_${policy.permission}`, object, context)
   → { decision: allowed }
```

### 4.2 Data Model Changes

변경 없음(정책은 `lazyfga-8`, 모델은 OpenFGA).

### 4.3 Core Logic

evaluate(req) — 결정적·단일 Check:

1. 인증: 유효한 service token 또는 admin 토큰 아니면 401.
2. 요청 검증: `subject.type/id`, `action.name`, `resource.type/id` 필수. 누락 시 400.
3. 정책 조회: `policy = findByActionResource(action.name, resource.type)`.
   - 없으면 → **decision:false** (deny-by-default). 응답에 사유 코드 `NO_POLICY`(상세 노출 최소). 200으로 반환(AuthZEN은 평가 성공/결정 분리; "정책 없음"은 평가 결과 deny).
4. OpenFGA Check 구성:
   - `user = subject.type + ":" + subject.id` (보통 `user:<id>`)
   - `relation = "can_" + policy.permission`
   - `object = resource.type + ":" + resource.id`
   - `context = req.context`(정책에 condition이 없으면 OpenFGA가 무시)
   - `authorizationModelId =` 현재 발행 버전(`current_model_version_id`의 model id)으로 핀 → decision과 reason(`lazyfga-11`)이 같은 모델 버전 사용
5. `allowed = openfga.check(...)`. OpenFGA가 relation/type 부재로 오류(모델 깨짐) → 500 + audit 경고(정책-모델 불일치, `lazyfga-8` 참고). 정상 → `decision = allowed`.
6. 반환: AuthZEN `{ decision }`. (M4에서 `context.reason` 추가)

신뢰 경계: 호출자는 `subject`를 임의 지정 가능 → 반드시 신뢰된 서비스(서비스 토큰 보유)만 호출. 미들웨어에서 강제.

### 4.4 AuthZEN 매핑 표 (오해 방지)

| AuthZEN 필드                | lazyFGA 사용                              |
| --------------------------- | ----------------------------------------- |
| `subject.type`,`subject.id` | OpenFGA user 식별자 `type:id`             |
| `action.name`               | policy.permission (relation `can_<name>`) |
| `resource.type`             | policy.resourceType / object type         |
| `resource.id`               | object 인스턴스 id                        |
| `context`                   | OpenFGA Check `context`(조건용, 통과)     |
| 응답 `decision`             | OpenFGA Check `allowed`                   |

## 5. API Design

### 5-1. New / Modified

```
POST /access/v1/evaluation     (AuthZEN 1.0 호환)
Authorization: Bearer <service token>
body:
{
  "subject":  { "type": "user", "id": "antegral" },
  "action":   { "name": "read" },
  "resource": { "type": "document", "id": "123" },
  "context":  { "ip": "10.0.0.4" }        // optional
}
→ 200 { "decision": true }
```

(편의 shorthand는 후속: `POST /policy/evaluate { id, subject, object, context }` — 내부적으로 동일 evaluate 호출. MVP는 AuthZEN 정식 엔드포인트 우선.)

```ts
// packages/shared/src/authzen.ts
export interface EvaluationRequest {
  subject: { type: string; id: string };
  action: { name: string };
  resource: { type: string; id: string };
  context?: Record<string, unknown>;
  options?: { reason?: boolean }; // reason=true면 응답 context.reason 부착(lazyfga-11)
}
export interface EvaluationResponse {
  decision: boolean;
  context?: Record<string, unknown>;
}
```

### 5-2. Error Handling

| Status                 | Description                                               |
| ---------------------- | --------------------------------------------------------- |
| 400                    | 필수 필드 누락/형식 오류                                  |
| 401                    | service/admin 토큰 없음·무효                              |
| 200 + `decision:false` | 정책 없음(`NO_POLICY`) 또는 관계 부재 → deny-by-default   |
| 500                    | OpenFGA Check 자체 오류(모델-정책 불일치 등) + audit 경고 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                 | Estimated | Owner |
| ------- | ---------------------------------------------------- | --------- | ----- |
| Phase 1 | AuthZEN 요청/응답 타입 + 엔드포인트 + 인증 연결      | 0.5d      | TBD   |
| Phase 2 | evaluate 코어(정책 조회 → Check 구성 → 결정)         | 1d        | TBD   |
| Phase 3 | deny-by-default·에러 처리 + 통합 테스트(픽스처 모델) | 0.5d      | TBD   |

### 6-2. Dependencies

- `lazyfga-8`(`PolicyRepo`), `apps/api/src/openfga`(`check`), 인증 미들웨어(`lazyfga-10`).

## 7. References

- [OpenID AuthZEN Authorization API 1.0](https://openid.github.io/authzen/)
- [openfga/authzen-interop](https://github.com/openfga/authzen-interop)
- [OpenFGA Relationship Queries (Check)](https://openfga.dev/docs/interacting/relationship-queries)
- `lazyfga-8`(정책), `lazyfga-11`(reason 추가)
