# Demo Scenario + Docs Finalization - Spec Proposal

| Item      | Detail                                      |
| --------- | ------------------------------------------- |
| Author    | Seonguk Moon                                |
| Created   | 2026-06-29                                  |
| Status    | **Implemented**                             |
| Reviewers | Claude (M7 cross-review; Codex unavailable) |

---

## 1. Summary

전체 흐름(모델 저작 → 조건 → 발행 → IdP 동기화 → 결정 → 설명 → playground → audit)을 하나로 꿰는 **재현 가능한 데모 시나리오 스크립트**와 **문서 마감**(README/getting-started/API 요약 + CONCEPT/ARCHITECTURE/ROADMAP/CLAUDE.md 동기화)을 제공한다. 심사위원/사용자가 `compose up` 후 한 흐름으로 제품 가치를 본다.

## 2. Background & Motivation

- 출품/평가에는 "한 번에 도는 데모"가 결정적이다. 기능은 `lazyfga-0~18`에 흩어져 있고, 이를 **한 시나리오**로 엮어야 가치가 드러난다(CONCEPT "한눈에 보는 흐름").
- 라이브 ZITADEL 없이도 데모가 돌아야 한다 → ZITADEL Actions V2 요청을 **로컬에서 올바르게 서명해 replay**하는 스크립트로 IdP 경로를 자체 완결로 시연(`lazyfga-16` 서명 구성 재사용).

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `scripts/demo/`: idempotent 시나리오 러너 — (1) 데모 모델 발행, (2) 정책 시드, (3) IdP 연결+매핑 규칙 시드(`lazyfga-16`), (4) **서명된 ZITADEL grant 이벤트 replay**로 tuple 동기화, (5) 필요한 직접 tuple 시드.
- [ ] `scripts/demo/reset`: 데모 상태 초기화(정책·IdP 설정·tuple 정리; OpenFGA store/모델은 안내).
- [ ] README 마감: quickstart(compose up · env · web/api 실행) + 데모 워크스루 + **5 기둥 ↔ 기능 매핑**.
- [ ] `docs/` getting-started + **API 요약**(`/model`·`/policies`·`/tokens`·`/access/v1/evaluation`·`/idp/*`·`/audit`).
- [ ] CONCEPT/ARCHITECTURE/ROADMAP **상태 동기화** + `CLAUDE.md` File Structure 트리 갱신(신규 디렉터리/스크립트 반영).

### 3.2 Non-Goals

- [ ] 운영 배포 가이드(k8s/TLS/스케일링), 성능 벤치마크, 멀티테넌시.
- [ ] 신규 런타임 기능. 본 명세는 **기존 기능의 오케스트레이션 + 문서**만 한다(코드 신규 엔드포인트 없음).
- [ ] 라이브 ZITADEL 자동 프로비저닝(Target/Execution 생성은 문서로 안내; replay 스크립트로 대체 시연).

## 4. Technical Design

### 4.1 Architecture Overview

```
scripts/demo/run.ts  (admin 토큰으로 lazyFGA API 호출 + 서명 replay)
  1) POST /model        ← 데모 IR(team/project + document/folder, 조건 포함 옵션)  발행
  2) POST /policies      ← can_read/document 등 정책
  3) POST /idp/connections + /rules  ← provider=zitadel, signing_secret, projectId 기반 규칙(lazyfga-16)
  4) replay: 서명된 user.grant.added payload → POST /idp/webhook/zitadel → user:alice member team:P (audited)
  5) 직접 구조 tuple 시드(필수): 역할 바인딩(team:P#member viewer folder:F)·상속 부모(folder:F parent document:1)를
     OpenFGA SDK로 직접 기록 (매핑 엔진 주입 제약상 webhook으론 불가)
  ▶ 출력: evaluate/explain 호출 예시(curl) + playground 안내
```

### 4.2 Data Model Changes

신규 스키마 없음. 데모 모델 IR과 시드 데이터는 스크립트 입력(코드/픽스처). 기존 `docFolderTeamIR`를 기반으로 데모용 IR을 구성(조건 1개 포함해 M5도 시연).

### 4.3 Core Logic

- **idempotent**: 재실행 안전. 단 (a) 모델 발행은 매 호출 새 버전(`lazyfga-7`)이라 버전 증가(의도된 예외), (b) 연결(`provider` UNIQUE)·정책(`UNIQUE`)은 GET-then-PUT/존재 확인으로, (c) 매핑 규칙은 unique 키가 없어 POST 반복 시 중복되므로 **clear-then-insert**(또는 내용 매치)로 멱등화. 실패 단계는 명확히 보고.
- **서명 replay**: 데모는 연결의 `signing_secret`을 알므로 `lazyfga-16`의 **구현된 서명 헬퍼**(문서에서 재유도 금지)로 `ZITADEL-Signature`를 **현재 `t`로**(5분 윈도우 내) 만들어 실제 ZITADEL 없이 `/idp/webhook/zitadel`를 통과시킨다(자체 완결 시연; 여기서 'replay'=데모 재현, `lazyfga-16`의 replay-방지와 무관). 실제 ZITADEL 연동은 문서로 안내(`lazyfga-16` §4.4).
- **tuple 출처 구분(중요)**: ① IdP replay → `user:alice member team:P`(webhook 경로 → `idp.tuple.write`로 audit). ② 구조 tuple(`team:P#member viewer folder:F`, `folder:F parent document:1`) → `lazyfga-15` 매핑 엔진은 userset(`#`)·임의 객체 주입을 막으므로 **데모 스크립트가 OpenFGA SDK로 직접** 기록(런타임 엔드포인트 아님; Q4=A 시드). 직접 기록분은 webhook 비경유라 `/audit`에 없음.
- **데모 모델**: team(group) + project 매핑(IdP grant→team#member) + document/folder(role×permission, 상속) + 조건 1개(예: 업무시간) → M1~M5를 한 모델로 시연. **조건은 시연 allow 경로 밖**(별도 역할/리소스)에 두거나, 경로에 둘 경우 evaluate에 필요한 context를 함께 전달한다(누락 시 500, `lazyfga-14`).
- **5 기둥 매핑(문서)**: ① 노드 모델 저작(`lazyfga-5/6`) ② 조건(`lazyfga-13/14`) ③ named policy PDP(`lazyfga-8/9`) ④ explainability(`lazyfga-11/12`) ⑤ IdP 연동(`lazyfga-15/16`) + 운영(audit `lazyfga-17`, playground `lazyfga-18`).
- **상태 동기화**: 구현 완료된 proposal Status=Implemented 반영, ROADMAP 출품 우선순위 갱신, ARCHITECTURE 디렉터리 트리 실제화, `CLAUDE.md` File Structure 트리에 `modules/idp`·`features/condition-builder|playground|audit`·`scripts/demo`·`docs/` 추가. CLAUDE.md 규칙("파일 구조 변경 시 이 파일 먼저 갱신")에 따라 구조 추가는 스크립트/문서 생성과 **함께(또는 직전)** CLAUDE.md를 갱신한다.

### 4.4 데모 워크스루(문서 순서)

1. `docker compose up -d` → web/api 실행.
2. admin 토큰 발급 → 데모 스크립트 실행.
3. web: 캔버스에서 모델 확인 → 행렬 → 조건 빌더.
4. playground/explain: "alice가 document:1 read?" → allow + 경로(IdP grant→team→상속).
5. audit: 방금의 `model.publish`와 **IdP 동기화 membership**(`idp.tuple.write`) 변경 확인(스크립트가 SDK로 직접 시드한 구조 tuple은 webhook 비경유라 audit에 없음 — 의도된 동작).

## 5. API Design

### 5-1. New / Modified

신규 lazyFGA REST API 없음. 스크립트는 기존 lazyFGA 엔드포인트를 호출하고, **구조 tuple만 OpenFGA SDK로 직접** 기록한다(런타임 엔드포인트 추가 아님). 산출물:

- `scripts/demo/run.ts`, `scripts/demo/reset.ts`, 데모 IR/픽스처.
- `README.md`(마감), `docs/getting-started.md`, `docs/api.md`(엔드포인트 요약).

### 5-2. Error Handling

| 상황                 | 처리                                               |
| -------------------- | -------------------------------------------------- |
| 스택 미기동          | 스크립트가 `/healthz` 선검사 후 명확한 안내로 중단 |
| admin 토큰 부재/무효 | 401 안내 후 중단                                   |
| 모델/정책 시드 실패  | 단계·사유 출력 후 중단(부분 적용 방지 안내)        |
| replay 서명 불일치   | 연결 secret 불일치 안내(설정 점검)                 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                                                                     | Estimated | Owner |
| ------- | -------------------------------------------------------------------------------------------------------- | --------- | ----- |
| Phase 1 | 데모 IR/정책/규칙 시드 러너 + idempotent + `/healthz` 선검사                                             | 1d        | TBD   |
| Phase 2 | 서명 replay(ZITADEL grant→tuple) + reset 스크립트                                                        | 1d        | TBD   |
| Phase 3 | README/getting-started/api 문서 + CONCEPT/ARCHITECTURE/ROADMAP/CLAUDE.md 동기화 + 전체 E2E 워크스루 검증 | 1d        | TBD   |

### 6-2. Dependencies

- 전 단계 전부(`lazyfga-0~18`): 발행(`7`)·정책(`8`)·evaluate(`9`)·admin/service 토큰(`10`)·explain(`11/12`)·조건(`13/14`)·IdP(`15/16`)·audit(`17`)·playground(`18`).
- 실행 환경: docker compose(openfga+postgres), admin 토큰. (`.env` 포트 우회는 배포별.)
- ROADMAP은 M5(조건)·M7 마감을 "여유 시"로 두지만 본 capstone의 조건 시연은 **M5 구현을 전제**한다(M5 미구현 시 조건 단계 생략 가능).

## 7. References

- [CONCEPT.md](../CONCEPT.md) "한눈에 보는 흐름", 5 기둥
- [ROADMAP.md](../ROADMAP.md) 출품 우선순위, [ARCHITECTURE.md](../ARCHITECTURE.md) `scripts/`(seed·demo)
- `lazyfga-16` §4.4(ZITADEL 연동 절차·서명 구성), `lazyfga-17/18`
