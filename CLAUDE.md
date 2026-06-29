# Agent Instructions
* All Coding-Agent must be plan first, code later. Always start with a detailed plan before writing any code.
* If change file structure, update this file first.

# Project Overview
* All AI agents must understand the project's concept and proceed with its design or implementation.
* For an overview of the project and its concept, please refer to [CONCEPT.md](CONCEPT.md).

# File Structure
> Updated through M7 (LFGA-0~19). Extend this tree as later work lands.

```
lazyfga/
├─ CONCEPT.md · ARCHITECTURE.md · ROADMAP.md · README.md   # 컨셉 / 구조 / 로드맵 / 진입
├─ package.json · pnpm-workspace.yaml · turbo.json
├─ tsconfig.base.json · eslint.config.js · .prettierrc.json
├─ docker-compose.yml · .env.example · .dockerignore
├─ proposals/                 # LFGA-N 구현 명세
├─ docs/                      # getting-started.md · api.md (LFGA-19)
├─ scripts/
│  └─ init-openfga-db.sql     # 단일 postgres 내 openfga DB 분리 생성
├─ apps/
│  ├─ web/                    # Vite + React + @xyflow/react + zustand
│  │  └─ src/
│  │     ├─ main.tsx · App.tsx · index.css
│  │     ├─ store/            # modelStore · explainStore (zustand)
│  │     └─ features/         # model-canvas · permission-matrix · explain
│  │                          #   · condition-builder · playground · audit
│  └─ api/                    # Hono on Bun (모듈러 모놀리스)
│     ├─ Dockerfile · drizzle.config.ts
│     ├─ scripts/             # seed-zitadel-rules.ts · demo/{run,reset}.ts (LFGA-16/19)
│     └─ src/
│        ├─ index.ts          # 부트스트랩 · 라우트 마운트 · /healthz
│        ├─ config.ts · middleware/auth.ts
│        ├─ db/               # Drizzle client · schema · migrations(0000~0005) · migrate
│        ├─ openfga/          # OpenFgaGateway (SDK 래퍼 + store 부트스트랩)
│        └─ modules/          # model · policy · pdp · auth · idp(+adapters/zitadel) · audit
└─ packages/
   ├─ shared/                 # 계약: model · ident · condition · authzen · policy · reason · audit · edit · fixtures
   └─ compiler/               # ★ 심장: ir-to-dsl · dsl-to-ir · coverage · condition-to-cel (isomorphic)
```

# Proposal Generation
* Proposal IDs are named LFGA-X. (X starts from 0, increases sequentially, and is unique across all proposals.)
* The proposal's status field can only contain `**Draft**` / `**In Review**` / `**Approved**` / `**Implemented**` and it must be updated in real time whenever a new stage is reached.
* The author of all proposals will be set to "Seonguk Moon", All proposals must be written in English.
* When generating a proposal, do not use questioning tools. Output the text for each section and wait for the user's response.
* A proposal must always be free of logical contradictions in all its content.
* Once the proposal is complete, a separate agent is launched to double-check for any logical inconsistencies in the proposal.
* If logical inconsistencies are found in the proposal, and if there are only minor changes to the user's proposed plan, structure, and flow, the proposal should be revised in accordance with the recommended actions based on best practices.

# Proposal-Driven Development
When proceeding with implementation based on a previously written proposal, development must strictly follow the sequence below.

## 1. Proposal Pre-inspection
* Inspect whether the code scope affected or modified is logically consistent and free of contradictions with the content defined in the proposal.
* If a logical contradiction is discovered within the proposal, modify that part of the proposal and report it to the user.
* In addition, if the structural changes from the original proposal are significant, you must obtain user approval before proceeding.

## 2. Implementation
* If the proposal aligns with the codebase analysis results from step 1 without logical contradictions, proceed with the implementation.
* To synchronize the implementation order with the proposal, if a Todo tool is available, divide and proceed with the implementation based on each Phase unit specified in the Milestones section.
* If testable cases exist at the code level, aim to implement them as much as possible to practice TDD, even if specific test cases are not explicitly stated in the proposal.

## 3. Code Review
* Once the implementation is complete, spawn a separate, independent agent to validate and verify the artifacts.
* The test agent must spawn the Claude and Codex agents in parallel and conduct separate reviews for each.
* If Codex's quota is exhausted, use only Claude reviews to receive feedback.
* All feedback received from the review agent must be corrected in full; no re-reviews will be conducted after corrections are made.

## 4. Testing
* Once implementation and code review are complete, proceed with testing.
* If you have implemented Web UI features, use Chrome DevTools MCP to run all end-to-end (E2E) tests.

## 5. After Implementation
* Ensure that all processes are carried out sequentially and that a commit is made once the implementation phase is complete.
* Commit messages must follow conventional commit rules and be written in English.
* In addition, we do not use em-dashes, and all commit messages always begin with a lowercase letter.
* The commit scope must NEVER be a proposal ID (e.g. `docs(LFGA-40)`, `test(LFGA-38)` are forbidden). The scope must always name the affected area or domain (e.g. `proposal`, `agent`, `api`, `webui`, `memory`, `provider`). The proposal ID belongs only in the subject text and/or the `Refs:` footer, never in the scope.
* The following is an example of a LFGA-1 commit message. It assumes that only the implementation of part of the proposal (Phase 1) has been completed:
```
feat(auth): implement login flow

Part 1 of LFGA-1: covers login only.
* MFA and password reset will follow in subsequent commits.

Refs: LFGA-1
```

# MCP Instruction

## Chrome Devtools MCP
* Since the Chrome DevTools MCP renderer retrieves data rendered on a separate server via SSH, you must set the IP address to `100.75.251.90` when opening a tab in the browser.
* Ports `52290` and `52291` are automatically forwarded via SSH to enable debugging that is only possible on localhost. Use them as needed.
