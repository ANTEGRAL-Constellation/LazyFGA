# Agent Instructions

- All Coding-Agent must be plan first, code later. Always start with a detailed plan before writing any code.
- If change file structure, update this file first.

# Project Overview

- All AI agents must understand the project's concept and proceed with its design or implementation.
- For an overview of the project and its concept, please refer to [CONCEPT.md](CONCEPT.md).

# File Structure

> Updated through M9 (LFGA-0~27). Extend this tree as later work lands.

```
lazyfga/
├─ CONCEPT.md · ARCHITECTURE.md · ROADMAP.md · README.md   # 컨셉 / 구조 / 로드맵 / 진입
├─ package.json · pnpm-workspace.yaml · turbo.json         # node 워크스페이스(web + packages)
├─ tsconfig.base.json · eslint.config.js · .prettierrc.json
├─ docker-compose.yml · .env.example · .dockerignore       # 4서비스: postgres·openfga·lazyfga-api·web(SPA/nginx); ADMIN_TOKEN 필수(:?), WEB_PORT 기본 5173
├─ proposals/                 # LFGA-N 구현 명세
├─ docs/                      # getting-started.md · api.md (LFGA-19)
├─ scripts/
│  └─ init-openfga-db.sql     # 단일 postgres 내 openfga DB 분리 생성
├─ apps/
│  ├─ web/                    # Vite + React + @xyflow/react + zustand (TS 유지)
│  │  ├─ Dockerfile           # multi-stage: node22+pnpm 빌드 → nginx:alpine 정적 서빙
│  │  ├─ nginx.conf           # SPA + 같은 출처 /api 프록시(→ lazyfga-api:8787, /api prefix strip)
│  │  └─ src/
│  │     ├─ main.tsx · App.tsx · index.css
│  │     ├─ store/            # modelStore · explainStore (zustand)
│  │     └─ features/         # model-canvas · permission-matrix · explain
│  │                          #   · condition-builder · playground · grants(LFGA-20) · audit
│  └─ api/                    # ★ Go 백엔드(LFGA-22~27에서 TS(Hono/Bun) 전체 대체; chi + pgx)
│     ├─ Dockerfile           # golang:1.25 → distroless static (+`healthcheck` 바이너리 모드)
│     ├─ go.mod · go.sum      # module github.com/antegral-constellation/lazyfga/api
│     ├─ scripts/coverage-gate.sh   # 커버리지 하드 게이트(≥95%, -count=1·-race·-coverpkg)
│     ├─ cmd/                 # lazyfga-api(서버) · demo(run|reset) · seed-zitadel-rules
│     └─ internal/
│        ├─ app/              # 부트스트랩 · 전 모듈 마운트 · degraded/graceful shutdown
│        ├─ httpx/            # chi 라우터 · 인증 미들웨어 · BodyLimit · Hono 호환 404/500
│        ├─ config/ · jsontime/ · jsutil/  # env · JS toISOString · JS 문자열/숫자 직렬화
│        ├─ db/               # pgx pool · drizzle 호환 마이그레이터(내장 migrations 0000~0006)
│        ├─ openfga/          # Gateway(go-sdk) · writeerror(분류, idp+permission 공유)
│        ├─ audit/            # fire-and-forget 감사 기록기(쓰기 측)
│        ├─ contract/         # shared 계약 포트: IR·조건·grant 검증기 + 바이트 호환 마샬러
│        ├─ compiler/         # ir-to-dsl(공식 openfga/language transformer) · condition-to-cel
│        ├─ democli/ · zitadelsign/ · testutil/
│        └─ modules/          # model · policy · pdp(+reason) · permission · auth · auditread
│                             #   · idp = signature · extraction · presets · mapping(설정형)
└─ packages/                  # TS 계약/컴파일러는 web 소비용으로 유지(백엔드는 Go 포트 사용)
   ├─ shared/                 # 계약: model · ident · condition · authzen · policy · reason · audit · edit · grant · fixtures
   │                          #   └ __fixtures__/parity/ = TS↔Go 크로스 언어 parity corpus(LFGA-24)
   └─ compiler/               # ir-to-dsl · dsl-to-ir · coverage · condition-to-cel (웹 미리보기용;
                              #   발행 시 권위 검증은 Go internal/compiler — drift는 parity corpus가 방지)
```

# Proposal Generation

- Proposal IDs are named LFGA-X. (X starts from 0, increases sequentially, and is unique across all proposals.)
- The proposal's status field can only contain `**Draft**` / `**In Review**` / `**Approved**` / `**Implemented**` and it must be updated in real time whenever a new stage is reached.
- The author of all proposals will be set to "Seonguk Moon", All proposals must be written in English.
- When generating a proposal, do not use questioning tools. Output the text for each section and wait for the user's response.
- A proposal must always be free of logical contradictions in all its content.
- Once the proposal is complete, a separate agent is launched to double-check for any logical inconsistencies in the proposal.
- If logical inconsistencies are found in the proposal, and if there are only minor changes to the user's proposed plan, structure, and flow, the proposal should be revised in accordance with the recommended actions based on best practices.

# Proposal-Driven Development

When proceeding with implementation based on a previously written proposal, development must strictly follow the sequence below.

## 1. Proposal Pre-inspection

- Inspect whether the code scope affected or modified is logically consistent and free of contradictions with the content defined in the proposal.
- If a logical contradiction is discovered within the proposal, modify that part of the proposal and report it to the user.
- In addition, if the structural changes from the original proposal are significant, you must obtain user approval before proceeding.

## 2. Implementation

- If the proposal aligns with the codebase analysis results from step 1 without logical contradictions, proceed with the implementation.
- To synchronize the implementation order with the proposal, if a Todo tool is available, divide and proceed with the implementation based on each Phase unit specified in the Milestones section.
- If testable cases exist at the code level, aim to implement them as much as possible to practice TDD, even if specific test cases are not explicitly stated in the proposal.

## 3. Code Review

- Once the implementation is complete, spawn a separate, independent agent to validate and verify the artifacts.
- The test agent must spawn the Claude and Codex agents in parallel and conduct separate reviews for each.
- If Codex's quota is exhausted, use only Claude reviews to receive feedback.
- All feedback received from the review agent must be corrected in full; no re-reviews will be conducted after corrections are made.

## 4. Testing

- Once implementation and code review are complete, proceed with testing.
- If you have implemented Web UI features, use Chrome DevTools MCP to run all end-to-end (E2E) tests.

## 5. After Implementation

- Ensure that all processes are carried out sequentially and that a commit is made once the implementation phase is complete.
- Commit messages must follow conventional commit rules and be written in English.
- In addition, we do not use em-dashes, and all commit messages always begin with a lowercase letter.
- The commit scope must NEVER be a proposal ID (e.g. `docs(LFGA-40)`, `test(LFGA-38)` are forbidden). The scope must always name the affected area or domain (e.g. `proposal`, `agent`, `api`, `webui`, `memory`, `provider`). The proposal ID belongs only in the subject text and/or the `Refs:` footer, never in the scope.
- The following is an example of a LFGA-1 commit message. It assumes that only the implementation of part of the proposal (Phase 1) has been completed:

```
feat(auth): implement login flow

Part 1 of LFGA-1: covers login only.
* MFA and password reset will follow in subsequent commits.

Refs: LFGA-1
```

# MCP Instruction

## Chrome Devtools MCP

- Since the Chrome DevTools MCP renderer retrieves data rendered on a separate server via SSH, you must set the IP address to `100.75.251.90` when opening a tab in the browser.
- Ports `52290` and `52291` are automatically forwarded via SSH to enable debugging that is only possible on localhost. Use them as needed.
