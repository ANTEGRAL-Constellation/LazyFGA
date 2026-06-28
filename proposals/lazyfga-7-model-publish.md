# Model Publish (Write Auth Model + 버전/diff) - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M2 cross-review)  |

---

## 1. Summary

캔버스에서 만든 `ModelIR`을 서버에서 컴파일해 OpenFGA에 새 authorization model로 기록(WriteAuthorizationModel)하고, 버전 메타(IR/DSL 스냅샷)를 lazyFGA Postgres에 저장한다. 버전 목록·조회·구조 diff를 제공한다.

## 2. Background & Motivation

- OpenFGA authorization model은 immutable/versioned이라, 발행 = "새 버전 생성"이다. 어떤 IR이 어떤 model id가 됐는지 추적이 필요(데이터 소유 분리: 의도=lazyFGA, 실행=OpenFGA).
- diff는 "이 변경이 무엇을 바꾸나"를 보여줘 편집 두려움을 낮춘다.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `POST /model`: IR 수신 → `validateModelIR` → `compileIrToDsl` → OpenFGA `WriteAuthorizationModel` → 버전 메타 저장 → 새 버전 반환.
- [ ] `GET /model/current`, `GET /model/versions`, `GET /model/versions/:id`.
- [ ] `GET /model/diff?from=:a&to=:b`: 두 버전 IR의 **구조 diff**(type/role/permission/parent 추가·삭제·변경).
- [ ] "current" 버전 포인터 관리(단일 store, 항상 최신 발행본).

### 3.2 Non-Goals

- [ ] **tuple 영향 분석**("이 변경으로 N명 접근 변동")은 비목표(후속). MVP diff는 구조 변경만.
- [ ] 모델 롤백 UI(메타는 남기되 자동 롤백은 후속).

## 4. Technical Design

### 4.1 Architecture Overview

```
web → POST /model(IR) → model.service
   → validateModelIR → compileIrToDsl → openfga.writeAuthorizationModel
   → INSERT model_version(ir,dsl,authorizationModelId) → audit(lazyfga-17)
   → { version }
```

### 4.2 Data Model Changes

신규 테이블 `model_version`:
| column | type | 설명 |
|--------|------|------|
| id | uuid PK | lazyFGA 버전 id |
| authorization_model_id | text | OpenFGA가 반환한 model id |
| ir_json | jsonb | 발행 시점 ModelIR 스냅샷 |
| dsl | text | 컴파일된 DSL |
| note | text null | 사용자 메모 |
| created_at | timestamptz | |
| created_by | text | admin/token 식별자 |

`instance_config`(lazyfga-1)에 `current_model_version_id` 추가(최신 발행본 포인터).

### 4.3 Core Logic

발행 절차(원자적 의도):
1. 입력 IR에 `validateModelIR` → 위반 시 422, 발행 중단.
2. `compileIrToDsl(ir)` → `{dsl, model}`. 실패 시 422.
3. `openfga.writeAuthorizationModel(model)` → `authorizationModelId`. (OpenFGA 자체 모델 검증도 통과해야 함; 실패 시 502로 표면화.)
4. `model_version` INSERT + `current_model_version_id` 갱신(같은 트랜잭션). OpenFGA write가 성공한 뒤 DB 기록이 실패하면 보상 불가하므로, **OpenFGA write를 먼저 수행하고 그 id를 DB에 기록**한다(DB 실패 시 에러 반환하되 OpenFGA엔 고아 모델이 남을 수 있음 → audit 경고 로그. MVP 허용 트레이드오프). 이 부분실패로 latest(OpenFGA)≠current(DB)가 되면 `lazyfga-9` 결정은 latest, `lazyfga-11` reason·`lazyfga-8` 검증은 current(구) IR을 봐서 skew가 생긴다 → 수동 복구: OpenFGA `ReadAuthorizationModels`로 최신 model id 확인 후 `current_model_version_id` 직접 갱신(운영 문서).
5. audit 이벤트 기록.

구조 diff(`from`,`to` 두 IR 비교):
- type 집합 차집합 → added/removed types.
- 공통 type 내 roles/permissions/parents 집합 비교 → added/removed/changed(특히 permission.grantedByRoles 변경은 "권한 확대/축소"로 라벨).
- 출력은 사람이 읽는 항목 리스트(JSON). 결정적 정렬.

## 5. API Design

### 5-1. New / Modified

```
POST /model            body: { ir: ModelIR, note?: string }
  → 201 { version: { id, authorizationModelId, createdAt } }

GET  /model/current    → 200 { version, ir, dsl }
GET  /model/versions   → 200 { versions: [{id, authorizationModelId, createdAt, note}] }
GET  /model/versions/:id → 200 { version, ir, dsl } | 404
GET  /model/diff?from=&to= → 200 { changes: DiffChange[] } | 404
```
모든 엔드포인트는 admin 인증 필요(`lazyfga-10`). (M2에선 로컬 전용으로 빌드 후 `lazyfga-10`/M3에서 가드 부착.)

```ts
type DiffChange =
  | { kind: "TYPE_ADDED"|"TYPE_REMOVED"; type: string }
  | { kind: "ROLE_ADDED"|"ROLE_REMOVED"; type: string; role: string }
  | { kind: "PERMISSION_ADDED"|"PERMISSION_REMOVED"; type: string; permission: string }
  | { kind: "GRANT_CHANGED"; type: string; permission: string; added: string[]; removed: string[] }
  // 아래 두 종류는 교차리뷰에서 추가(인가에 영향을 주는 변경을 diff에 노출):
  | { kind: "ROLE_ASSIGNABLE_CHANGED"; type: string; role: string; added: string[]; removed: string[] }
  | { kind: "PERMISSION_INHERIT_CHANGED"; type: string; permission: string; added: string[]; removed: string[] }
  | { kind: "PARENT_ADDED"|"PARENT_REMOVED"; type: string; relationName: string; parentType: string };
```

### 5-2. Error Handling

| Status | Description |
|--------|-------------|
| 401 | 인증 실패(admin 토큰 없음/오류) |
| 403 | service 토큰으로 control-plane 접근(역할 부족) |
| 422 | IR 검증 실패 또는 컴파일 실패(`ValidationError[]`/`CompileError` 동반) |
| 404 | 존재하지 않는 버전 id |
| 502 | OpenFGA WriteAuthorizationModel 실패 |
| 500 | DB 기록 실패(OpenFGA 고아 모델 가능 → audit 경고) |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                            | Estimated | Owner |
|---------|-------------------------------------------------|-----------|-------|
| Phase 1 | `model_version` 스키마 + 발행 절차(1~5)           | 1.5d      | TBD   |
| Phase 2 | 버전 조회 엔드포인트 + current 포인터              | 0.5d      | TBD   |
| Phase 3 | 구조 diff 구현 + 테스트                           | 1d        | TBD   |

### 6-2. Dependencies

- `packages/compiler`(`compileIrToDsl`), `packages/shared`(`validateModelIR`).
- `apps/api/src/openfga`(`writeAuthorizationModel`, `lazyfga-1`), `drizzle-orm`.
- 인증 미들웨어(`lazyfga-10`).

## 7. References

- [ARCHITECTURE.md](../ARCHITECTURE.md) — 데이터 소유 분리
- [OpenFGA: Configure Authorization Model / Write](https://openfga.dev/docs/getting-started/configure-model)
- `lazyfga-2/3`(IR/compiler)
