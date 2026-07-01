# Playground (inline assertion testing) - Spec Proposal

| Item      | Detail                                                              |
| --------- | ------------------------------------------------------------------- |
| Author    | Seonguk Moon                                                        |
| Created   | 2026-06-29                                                          |
| Status    | **Implemented**                                                     |
| Reviewers | Claude (M7 cross-review + adversarial re-review; Codex unavailable) |

---

## 1. Summary

발행된 모델·정책에 대해 _"alice가 document:1 을 read 할 수 있나?"_ 같은 인가 질의를 **그 자리에서 여러 건 테스트**하는 web 패널이다. 각 케이스를 AuthZEN evaluate로 실행해 allow/deny(+ 선택 기대값 대비 pass/fail)를 표로 보여주고, `lazyfga-12` explain을 재사용해 결정 경로를 펼친다. (CONCEPT 설계원칙 "예시(assertion)로 그 자리에서 테스트")

## 2. Background & Motivation

- CONCEPT: 추상적 relation을 자연어로 역번역하고 **예시로 즉시 검증**한다. `lazyfga-12` explain은 단건 "왜?"를 보여주지만, 모델을 다듬을 때 필요한 건 **여러 시나리오를 한 번에** 돌려보는 회귀 테스트형 surface다.
- tuple 데이터는 IdP 동기화(`lazyfga-15/16`) 또는 `scripts/` 시드로 채워진다(Q4=A). playground는 그 데이터 위에서 **읽기 전용 질의**만 한다(tuple 생성/편집은 범위 밖).

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] web `features/playground`: 테스트 케이스 목록 편집(subject/action/resource/context + 선택 `expected` decision).
- [ ] "Run all": 각 케이스를 `POST /access/v1/evaluation`(service/admin 토큰)로 실행 → 결과 표(decision, expected 대비 pass/fail).
- [ ] 정책 기반 빠른 입력: action·resource.type을 **정의된 정책 목록**(`GET /policies`의 (permission, resourceType) 쌍)에서 선택(발행본과 정렬 → 무의미 질의 감소). 모델 IR은 비권위적 힌트로만 사용.
- [ ] 케이스별 "explain" 토글: `lazyfga-12` `useExplain`/캔버스 경로 하이라이트 재사용.
- [ ] 케이스 세트는 브라우저 로컬에 저장(localStorage)해 새로고침 후 유지.

### 3.2 Non-Goals

- [ ] tuple 생성/편집 UI. 데이터는 IdP 동기화/시드 스크립트로(Q4=A). playground는 읽기 전용.
- [ ] **미발행 draft 모델**에 대한 평가. evaluate는 발행본(`current_model_version`)을 핀한다(`lazyfga-9`) — playground도 발행본을 테스트한다(드래프트 테스트는 후속).
- [ ] 케이스 세트의 서버 영속/공유(MVP는 로컬). 서버 저장·CI 어서션은 후속.
- [ ] 배치 evaluate 전용 엔드포인트(MVP는 클라이언트가 케이스별 호출).

## 4. Technical Design

### 4.1 Architecture Overview

```
web/features/playground
  CaseList: TestCase[] (subject, action, resource, context?, expected?)  ← localStorage 영속
  RunAll → 각 케이스: POST /access/v1/evaluation { subject, action, resource, context }   (service/admin 토큰)
         → ResultRow: decision + (expected 있으면) pass/fail 배지
  ExplainToggle(case) → useExplain(lazyfga-12) → reason + 캔버스 경로 하이라이트(단건)
  Pickers: action·resource.type ← GET /policies의 (permission, resourceType) 쌍(발행본 정렬). modelStore(IR)는 힌트만.
```

### 4.2 Data Model Changes

서버 변경 없음(기존 `lazyfga-9` evaluate, `lazyfga-8` 정책 조회 재사용). 케이스 세트는 브라우저 localStorage(서버 스키마 아님).

### 4.3 Core Logic

- **TestCase**: `{ subject:{type,id}, action:{name}, resource:{type,id}, context?, expected?: boolean }`. AuthZEN evaluate 요청 형태 그대로 + 선택 기대값.
- **Run**: 케이스별 evaluate 호출. 응답 `decision`을 표시. `expected`가 있으면 `decision===expected`로 pass/fail. (정책 미존재면 evaluate가 `decision:false`/NO_POLICY → deny로 표시, `lazyfga-9`.)
- **결정성/순서**: 케이스 순서대로 실행하고 결과를 케이스에 1:1 매핑. 실패한 단건(네트워크/HTTP) 케이스는 "error"로 표기하고 나머지는 계속.
- **explain 재사용(단건)**: explain 토글은 `useExplain.run(caseReq)`를 그대로 호출한다(`useExplain`이 내부에서 `options.reason=true`를 강제하므로 별도 설정 불필요) → reason 카드 + 캔버스 하이라이트. **단건만 활성**(하이라이트 충돌 방지). **runAll은 `useExplain`를 거치지 않고 evaluate를 직접 호출**한다(배치 상태 clobber 방지).
- **캔버스 하이라이트 정합성(중요)**: explain 경로는 **발행본** 모델 기준으로 계산되지만 캔버스(`modelStore.ir`)는 편집 가능한 로컬 버퍼다. 둘이 다르면 하이라이트가 어긋날 수 있으므로 패널에 **정적 경고**를 상시 노출한다("explain은 현재 캔버스를 강조하며 발행본과 다를 수 있음"). 발행본 대조 기반 자동 억제는 published-model 참조(admin 전용 GET /model/current)가 필요해 후속으로 둔다 — MVP는 경고로 정직하게 표기.
- **빈 데이터 안내**: tuple이 없으면 모두 deny가 정상임을 패널에 안내하고, 시드 스크립트/IdP 동기화로 데이터를 채우라고 링크(Q4=A).
- **action/resource 픽커**: `GET /policies`로 (permission, resourceType) 조합을 받아 action·resource.type 후보를 채운다(발행본 정렬). 자유 입력도 허용하되, 정책 없는 조합은 NO_POLICY deny가 됨을 표시.

### 4.4 Web 통합

App 레이아웃에 playground 패널 추가(캔버스/행렬/explain와 공존). evaluate에는 service/admin 토큰이 필요하므로 `usePlayground`가 토큰을 보유한다(`token`/`setToken`); UI는 explain 패널과 같은 토큰 입력을 공유한다(공유 store 또는 상위 prop으로 주입).

## 5. API Design

### 5-1. New / Modified

신규 REST 없음. 재사용: `POST /access/v1/evaluation`(`lazyfga-9`), `GET /policies`(`lazyfga-8`).

```ts
// apps/web/src/features/playground
export interface TestCase {
  subject: { type: string; id: string };
  action: { name: string };
  resource: { type: string; id: string };
  context?: Record<string, unknown>;
  expected?: boolean;
}
export function usePlayground(): {
  token: string;
  setToken(t: string): void; // evaluate 호출용(service/admin), explain과 공유
  cases: TestCase[];
  setCases(next: TestCase[]): void; // localStorage 동기화
  runAll(): Promise<void>; // token으로 인증, evaluate를 케이스별 직접 호출
  results: Array<{ decision?: boolean; pass?: boolean; error?: string }>;
  running: boolean;
};
```

### 5-2. Error Handling

| 상황                        | 처리                                       |
| --------------------------- | ------------------------------------------ |
| evaluate 비2xx(400/401/5xx) | 해당 케이스 결과 "error" 표기, 나머지 계속 |
| 정책 없음(NO_POLICY)        | deny로 표시(평가 결과; `lazyfga-9`)        |
| tuple 없음                  | 모두 deny(정상) + 데이터 시드 안내         |
| 토큰 미입력                 | 실행 비활성 + 안내                         |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                        | Estimated | Owner |
| ------- | --------------------------------------------------------------------------- | --------- | ----- |
| Phase 1 | TestCase 모델 + CaseList 편집 + localStorage 영속                           | 0.5d      | TBD   |
| Phase 2 | RunAll(케이스별 evaluate) + 결과 표 + expected pass/fail                    | 1d        | TBD   |
| Phase 3 | explain 재사용 토글 + action/resource 픽커(정책·모델) + 빈데이터 안내 + E2E | 1d        | TBD   |

### 6-2. Dependencies

- `lazyfga-9`(evaluate), `lazyfga-12`(`useExplain`·캔버스 하이라이트), `lazyfga-8`(정책 목록), `lazyfga-5`(modelStore). 데이터: `lazyfga-15/16` 또는 `scripts/` 시드(Q4=A).

## 7. References

- [CONCEPT.md](../CONCEPT.md) 설계원칙("예시(assertion)로 그 자리에서 테스트")
- [ARCHITECTURE.md](../ARCHITECTURE.md) `web/features/playground`
- `lazyfga-9`(evaluate), `lazyfga-12`(explain), `lazyfga-8`(정책)
