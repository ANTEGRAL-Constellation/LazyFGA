# Monorepo Foundation - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M0 cross-review)  |

---

## 1. Summary

pnpm 워크스페이스 기반의 모노레포 골격을 구축한다. `apps/web`, `apps/api`, `packages/shared`, `packages/compiler` 네 워크스페이스와 공통 TypeScript·빌드·린트 툴링을 셋업하여, 이후 모든 기능이 올라갈 토대를 만든다.

## 2. Background & Motivation

- ARCHITECTURE.md의 4대 결정 중 두 가지("컴파일러를 `packages/`로 분리해 web·api가 공유", "모듈러 모놀리스")를 **코드 경계로 강제**하려면 워크스페이스 분리가 선행되어야 한다.
- `packages/compiler`(비주얼 IR ↔ DSL 변환)는 web(브라우저 실시간 미리보기)과 api(발행 시 권위 검증)가 동일 코드로 써야 drift가 없다 → 별도 패키지로 두고 양쪽이 의존.
- `packages/shared`로 타입·계약을 공유해 end-to-end 타입 안전을 확보한다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] pnpm workspace + 4개 워크스페이스(`apps/web`, `apps/api`, `packages/shared`, `packages/compiler`) 생성.
- [ ] 공통 `tsconfig` base + 워크스페이스별 확장, ESLint/Prettier, 빌드 파이프라인(turbo) 구성.
- [ ] 의존성 방향 규칙 확립: `apps/* → packages/*`, `packages/compiler`는 외부 런타임 의존성 0(순수).
- [ ] `apps/api` 최소 부팅(Hono on Bun) + `GET /healthz` 200 응답.
- [ ] `apps/web` 최소 부팅(Vite + React) + 빈 화면 렌더.

### 3.2 Non-Goals

- [ ] 실제 기능 로직(컴파일러/PDP/UI)은 후속 명세에서 다룬다.
- [ ] CI/CD 파이프라인, 배포(=`lazyfga-1`에서 compose).

## 4. Technical Design

### 4.1 Architecture Overview

```
lazyfga/ (pnpm workspace root)
├─ apps/web        ─┐  의존
├─ apps/api        ─┤────▶ packages/shared  (타입·계약)
│                   └────▶ packages/compiler (IR↔DSL, 의존성 0)
└─ packages/*
```

- 런타임: api·compiler·shared는 Bun, web은 Vite(브라우저). compiler/shared는 브라우저·Bun 양쪽에서 import 되므로 **플랫폼 비의존 순수 TS**로 작성한다.
- 빌드 오케스트레이션: `turbo`로 `build`/`dev`/`test`/`typecheck` 태스크 파이프라인 구성.

### 4.2 Data Model Changes

변경 없음 (DB는 `lazyfga-1`에서 도입).

### 4.3 Core Logic

핵심 로직 없음(골격). 단, 의존성 방향을 lint 규칙(`eslint-plugin-boundaries` 또는 tsconfig path 제약)으로 강제한다:
1. `packages/compiler`는 `apps/*`, `packages/shared` 를 import 할 수 없다(순수·역의존 금지).
2. `packages/shared`는 어떤 워크스페이스도 import 하지 않는다(최하위 계약).
3. `apps/*`는 `packages/*`를 import 할 수 있으나 서로(`web↔api`)를 직접 import 하지 않는다(계약은 `shared` 경유).

## 5. API Design

### 5-1. New / Modified

REST 아님(부팅 확인용 1개만):

```
GET /healthz → 200 { "status": "ok", "version": <string> }
```

워크스페이스 패키지 경계(파일 단위):

```ts
// packages/shared/src/index.ts
// 모든 공유 타입의 단일 진입점. 런타임 코드 없음(타입 전용 export 지향).
export * from "./model";     // 후속: lazyfga-2
export * from "./authzen";   // 후속: lazyfga-9
export * from "./policy";    // 후속: lazyfga-8
export * from "./reason";    // 후속: lazyfga-11
export * from "./condition"; // 후속: lazyfga-14

// packages/compiler/src/index.ts
// 비주얼 IR ↔ OpenFGA DSL 변환의 단일 진입점. 외부 런타임 의존성 0.
export * from "./ir-to-dsl";       // 후속: lazyfga-3
export * from "./dsl-to-ir";       // 후속: lazyfga-4
export * from "./coverage";        // 후속: lazyfga-4
```

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| 워크스페이스 간 의존성 방향 위반 | lint 에러로 빌드 실패(런타임 아님) |
| `/healthz` 외 부팅 실패 | 프로세스 비정상 종료 + 로그 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                       | Estimated | Owner |
|---------|------------------------------------------------------------|-----------|-------|
| Phase 1 | pnpm workspace + 4 패키지 scaffold + tsconfig base         | 0.5d      | TBD   |
| Phase 2 | turbo 파이프라인 + ESLint/Prettier + 의존성 경계 규칙       | 0.5d      | TBD   |
| Phase 3 | api `/healthz`(Hono/Bun) + web 부팅(Vite/React)            | 0.5d      | TBD   |

### 6-2. Dependencies

- 런타임/툴: `bun`, `pnpm`, `turbo`, `typescript`, `eslint`, `prettier`.
- `apps/api`: `hono`. `apps/web`: `react`, `react-dom`, `vite`, `@vitejs/plugin-react`.

## 7. References

- [ARCHITECTURE.md](../ARCHITECTURE.md) — 디렉터리 구조 및 4대 결정
- pnpm workspaces, Turborepo, Hono, Vite 공식 문서
