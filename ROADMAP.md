# lazyFGA — Roadmap & Implementation Plan

> 근거 문서: [CONCEPT.md](./CONCEPT.md), [ARCHITECTURE.md](./ARCHITECTURE.md)
> 기능단위 구현 명세는 `proposals/lazyfga-<n>-<feature>.md` 에 작성된다.

## 확정된 컨셉 결정 (2026-06-28)

| 항목         | 결정                                                                                                |
| ------------ | --------------------------------------------------------------------------------------------------- |
| Tenancy      | **단일 OpenFGA store** (한 인스턴스 = 한 앱의 인가). 멀티테넌시는 범위 밖.                          |
| Named policy | **단일 질문 템플릿** — 정책 1개 = `(permission, resource type)` 하나, evaluate = OpenFGA Check 1회. |
| PDP API      | **OpenID AuthZEN 1.0 호환** 요청/응답.                                                              |
| Admin auth   | **단일 admin 토큰/계정** (MVP). 멀티유저·RBAC는 범위 밖.                                            |

## 빌드 원칙

**심장(compiler) → 비주얼 → 결정 → 연동.** `M1~M3` = 수직 슬라이스 MVP (모델을 그려 발행하고 실제 allow/deny가 나오는 최소 동작).

## 마일스톤 → 기능단위 → 명세 인덱스

| MS            | 기능단위                                           | 명세 파일                          | 의존                  |
| ------------- | -------------------------------------------------- | ---------------------------------- | --------------------- |
| **M0** 기반   | 모노레포 골격                                      | `lazyfga-0-monorepo-foundation`    | —                     |
|               | self-host compose (openfga+postgres)               | `lazyfga-1-selfhost-compose`       | 0                     |
| **M1** 심장   | 모델 IR (5-primitive)                              | `lazyfga-2-model-ir`               | 0                     |
|               | IR → OpenFGA DSL 컴파일                            | `lazyfga-3-ir-to-dsl-compiler`     | 2                     |
|               | DSL → IR 역변환 + coverage 경계                    | `lazyfga-4-dsl-to-ir-and-coverage` | 2,3                   |
| **M2** 비주얼 | model-canvas (React Flow) + 실시간 미리보기        | `lazyfga-5-model-canvas`           | 3,4                   |
|               | permission-matrix UI                               | `lazyfga-6-permission-matrix`      | 5                     |
|               | 모델 발행 API (OpenFGA WriteAuthModel + 버전/diff) | `lazyfga-7-model-publish`          | 3 (auth 가드는 10/M3) |
| **M3** 결정   | service token + admin auth (가드 미들웨어)         | `lazyfga-10-service-token-auth`    | 1                     |
|               | named policy 저장/관리                             | `lazyfga-8-named-policy-store`     | 1,7,10                |
|               | PDP evaluate (AuthZEN 1.0, Check 1회)              | `lazyfga-9-pdp-evaluate-authzen`   | 8,10                  |
| **M4** 설명   | reason 엔진 (Expand → allow path / deny 진단)      | `lazyfga-11-reason-engine`         | 9                     |
|               | explain 시각화 (web)                               | `lazyfga-12-explain-ui`            | 5,9,11                |
| **M5** 조건   | condition-builder UI (And/Or 블록)                 | `lazyfga-13-condition-builder-ui`  | 5                     |
|               | condition → CEL 컴파일 + 모델/정책 통합            | `lazyfga-14-condition-to-cel`      | 3,13                  |
| **M6** 연동   | IdP webhook 수신기 + adapter 인터페이스            | `lazyfga-15-idp-webhook-core`      | 1                     |
|               | ZITADEL adapter (flagship: 이벤트→tuple)           | `lazyfga-16-zitadel-adapter`       | 15                    |
| **M7** 마감   | audit log                                          | `lazyfga-17-audit-log`             | 1                     |
|               | playground (inline 테스트)                         | `lazyfga-18-playground`            | 9                     |
|               | 데모 시나리오 + 문서 마감                          | `lazyfga-19-demo-and-docs`         | 전부                  |
| **M8** 권한   | 구조적 grant/revoke/list                           | `lazyfga-20-permission-management` | 7,9,10                |
|               | IdP webhook 설정형 프레임워크                      | `lazyfga-21-idp-webhook-framework` | 15,16                 |
| **M9** Go     | Go 마이그레이션 마스터 플랜                        | `lazyfga-22-go-migration-master-plan` | 전부              |
|               | Go foundation (runtime·db·gateway)                 | `lazyfga-23-go-foundation`         | 22                    |
|               | contracts + compiler 포트 (parity corpus)          | `lazyfga-24-go-contracts-compiler` | 22 (∥23)              |
|               | core 모듈 (model·policy·pdp·grants)                | `lazyfga-25-go-core-modules`       | 23,24                 |
|               | platform 모듈 (tokens·audit·idp)                   | `lazyfga-26-go-platform-modules`   | 23,24 (∥25)           |
|               | 컷오버 (CLI·Docker·CI·docs·TS 제거)                | `lazyfga-27-go-cutover-ci`         | 23~26                 |

## 구현 현황 (2026-06-29)

**M0~M7 전부 구현 완료** — lazyfga-0~19 모든 proposal Status=Implemented. 각 단위는 사전검수 →
TDD → 교차리뷰(Claude) → 수정 → E2E → conventional commit 절차로 랜딩. 데모는
`apps/api/scripts/demo/run.ts`로 한 번에 시연(서명 webhook → membership → 상속 → ALLOW+reason).

## 출품 우선순위 (원안)

1. **반드시 (MVP·차별 핵심):** M0, M1, M2, M3, M4 — 비주얼 저작 + 발행 + 결정 + explainability.
2. **강력 권장 (IdP-agnostic 증명):** M6 (ZITADEL flagship).
3. **여유 시:** M5 (조건), M7 마감 품질.
