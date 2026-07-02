# lazyFGA — Architecture & Project Structure

> 스택: web = Vite+React+React Flow(TypeScript, pnpm), **api = Go(chi + pgx, 모듈러 모놀리스)**, DB = Postgres, OpenFGA = 별도 컨테이너. AuthZEN 호환 PDP. (M8/LFGA-22~27에서 백엔드를 Bun/Hono/Drizzle → Go로 이관; HTTP 계약·DB 스키마·포트(8787)·env 이름은 동일. Go 백엔드는 cutover swap 전까지 `apps/api`, swap 후 `apps/api`.)

---

## 구조를 좌우한 4가지 결정

1. **컴파일러 공유 → Go 이관 후엔 parity 코퍼스로 drift 방지.** 비주얼 모델 ↔ OpenFGA DSL 변환은 제품의 심장이다. 원래 web·api가 `packages/compiler` **같은 코드**를 써 drift가 없었다. M8에서 api가 Go로 이관되며 compiler(IR↔DSL, condition→CEL)와 contract 검증기가 Go로 포팅됐다: **발행 시 권위 검증은 Go 포트가 담당**하고, **브라우저 실시간 미리보기는 TS compiler를 그대로** 쓴다. 두 구현의 drift는 `packages/shared/src/__fixtures__/parity/` 골든 코퍼스(TS·Go 양측이 동일 케이스로 실행, CI parity job이 강제)로 막는다. 두 구현이 어긋나면 발행 시 검증이 이기고 코퍼스에 회귀 케이스를 추가한다. 컨셉의 "지원 범위 안에서만 양방향"은 web 전용 `compiler/coverage.ts`가 판정한다(dsl-to-ir/coverage는 web 전용이라 Go로 포팅하지 않는다).
2. **모듈러 모놀리스.** 한 api 서비스 안에 PDP·model·idp·policy·audit·auth를 모듈로 나눈다. 경계가 명확해 나중에 PDP만 떼어내기 쉽다.
3. **OpenFGA는 별도 컨테이너.** 자기 store/DB를 가진다. lazyFGA는 그 _위의_ 컨트롤 플레인이다 (OpenFGA를 대체하지 않는다는 컨셉 원칙과 일치).
4. **데이터 소유 분리.** 정책·audit·service token·모델 메타 = lazyFGA Postgres("사람이 만든 의도"). 관계 tuple·authorization model = OpenFGA("실행 진실").

---

## 디렉터리 구조

```
lazyfga/
├─ CONCEPT.md
├─ ARCHITECTURE.md
├─ package.json · pnpm-workspace.yaml · turbo.json
├─ docker-compose.yml          # openfga + postgres + lazyfga 한 방 self-host
├─ .env.example
│
├─ apps/
│  ├─ web/                      # Vite + React + React Flow (Visual-first UI)
│  │  └─ src/features/
│  │     ├─ model-canvas/       # 노드 캔버스: ResourceNode + ContainmentEdge(상속)
│  │     ├─ permission-matrix/  # 노드 더블클릭 → role×permission 행렬
│  │     ├─ condition-builder/  # WAF식 And/Or 블록 → (compiler) CEL
│  │     ├─ policy-studio/      # named policy 작성·발행
│  │     ├─ explain/            # 결정 경로 시각화 (allow path / deny 진단)
│  │     ├─ playground/         # "alice가 doc1 read?" inline 테스트
│  │     └─ audit/              # 변경 로그 뷰
│  │
│  └─ api/                      # Go: chi + pgx 모듈러 모놀리스 (LFGA-27에서 TS 대체)
│     ├─ Dockerfile · scripts/coverage-gate.sh
│     ├─ cmd/
│     │  ├─ lazyfga-api/        # 서버 엔트리포인트(+ `healthcheck` 컨테이너 모드)
│     │  ├─ demo/               # 데모 시나리오 run|reset (scripts/demo 포팅)
│     │  └─ seed-zitadel-rules/ # 매핑 규칙 시더
│     └─ internal/
│        ├─ app/                # 부트스트랩 · 모듈 마운트 · degraded/graceful shutdown
│        ├─ httpx/              # chi 라우터 · 인증 미들웨어 · JSON/BodyLimit · Hono 호환 404/500
│        ├─ config/ · jsontime/ · jsutil/   # env · JS toISOString · JS 문자열/숫자 직렬화
│        ├─ db/                 # pgx pool · drizzle 호환 마이그레이터(내장 SQL)
│        ├─ openfga/            # Gateway(go-sdk) · writeerror 분류
│        ├─ audit/              # fire-and-forget 감사 기록기(쓰기 측)
│        ├─ contract/           # shared 계약 포트: IR·조건·grant 검증기 + 바이트 호환 마샬러
│        ├─ compiler/           # ir-to-dsl(공식 transformer) · condition-to-cel
│        ├─ democli/ · zitadelsign/ · testutil/
│        └─ modules/            # model · policy · pdp(+reason) · permission · auth · auditread · idp
│
├─ packages/
│  ├─ shared/                   # end-to-end 타입·계약
│  │  └─ src/  model.ts(5 primitive IR) · authzen.ts · policy.ts · condition.ts
│  │
│  └─ compiler/                 # ★ 심장: 비주얼 IR ↔ OpenFGA DSL (의존성 0, 순수)
│     └─ src/
│        ├─ ir-to-dsl.ts        # IR(노드/행렬) → .fga DSL + AuthModel JSON
│        ├─ dsl-to-ir.ts        # .fga → IR (역방향, subset 안에서만)
│        ├─ matrix.ts           # role×permission → computed relation 생성
│        ├─ condition-to-cel.ts # 조건 트리 → CEL
│        └─ coverage.ts         # subset 경계 판정 (advanced → read-only)
│
└─ scripts/                     # seed · dev · ZITADEL 데모 시나리오
```

---

## 세 가지 핵심 흐름

```
[모델 저작]  web/model-canvas → IR → (compiler, 브라우저서 실시간 미리보기)
             └ 발행 시 → POST /model → model.service → (compiler 권위검증)
                         → OpenFGA WriteAuthModel(새 버전) → audit

[② 결정]    PEP → POST /access/v1/evaluation (service token) → pdp/evaluator
             → openfga Check(+CEL context) → reason(targeted Check/Read, M4) → AuthZEN 응답

[① 동기화]  IdP 이벤트 → POST /idp/webhook/:provider (서명검증) → idp/adapters/zitadel
             → claims/event 매핑 → openfga Write(tuple) → audit
```

---

## 컨셉 ↔ 구조 매핑

| 컨셉 기능             | 사는 곳                                                  |
| --------------------- | -------------------------------------------------------- |
| 노드 모델 저작        | `web/model-canvas` + `packages/compiler`                 |
| role×permission 행렬  | `web/permission-matrix` + `compiler/matrix.ts`           |
| WAF 조건 빌더         | `web/condition-builder` + `compiler/condition-to-cel.ts` |
| named policy PDP      | `api/modules/pdp` + `modules/policy`                     |
| explainability        | `web/explain` + `pdp/reason.ts`                          |
| IdP-agnostic 연동     | `api/modules/idp/adapters/*`                             |
| 비주얼 범위 경계      | `compiler/coverage.ts` (단일 진실)                       |
| audit · service token | `api/modules/audit` · `modules/auth`                     |

---

## 데이터 소유 (요약)

| 저장소           | 무엇을                                                                       | 비고               |
| ---------------- | ---------------------------------------------------------------------------- | ------------------ |
| lazyFGA Postgres | named policy, 모델 메타/버전 포인터, audit log, service token, IdP 연동 설정 | "사람이 만든 의도" |
| OpenFGA store    | authorization model, relation tuples, conditions                             | "실행 진실"        |

> 원칙: lazyFGA는 OpenFGA의 위에 있고, OpenFGA가 가진 정보(model/tuple)를 중복 저장하지 않는다. lazyFGA는 OpenFGA가 모르는 것(정책 이름, 의도, 감사, 토큰)만 가진다.
