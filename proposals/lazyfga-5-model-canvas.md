# Model Canvas (React Flow) + Live Preview - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M2 cross-review)  |

---

## 1. Summary

React Flow 캔버스에서 resource/group 타입을 노드로, 상속(containment)을 엣지로 그려 `ModelIR`을 시각적으로 저작하는 web 기능. IR이 바뀔 때마다 브라우저에서 `compileIrToDsl`(`lazyfga-3`)을 호출해 DSL을 실시간 미리보기한다.

## 2. Background & Motivation

- CONCEPT의 "매크로는 노드"(타입 간 관계) 부분. role×permission(마이크로)은 `lazyfga-6` 행렬이 담당.
- 캔버스 ↔ IR ↔ DSL을 한 화면에서 보여줘 "그림이 곧 모델"임을 체감시키는, 데모의 핵심 화면.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `web/features/model-canvas`: ResourceType=노드, GroupType=노드, ParentRef=엣지(document→folder)로 렌더/편집.
- [ ] 캔버스 조작(노드 추가/삭제/연결)이 단일 `ModelIR` 상태를 변형(single source of truth).
- [ ] IR 변경 시 디바운스 후 `compileIrToDsl` 호출 → DSL 패널 실시간 갱신 + `validateModelIR` 에러 인라인 표시.
- [ ] DSL에서 불러온 모델이 `coverage.fullyRepresentable=false`(`lazyfga-4`)면 캔버스를 read-only로 잠그고 advanced relation을 배지로 표시.

### 3.2 Non-Goals

- [ ] 노드 내부 role/permission 편집 UI(= `lazyfga-6`).
- [ ] 발행/서버 반영(= `lazyfga-7`). 본 기능은 클라이언트 상태까지.
- [ ] 조건 빌더(= `lazyfga-13`).

## 4. Technical Design

### 4.1 Architecture Overview

```
React Flow (nodes/edges)  ⇄  useModelGraph  ⇄  ModelIR(zustand store)
                                                   │ (debounced)
                                                   ▼
                                       compileIrToDsl  →  DSL 미리보기 패널
                                       validateModelIR →  에러 표시
                                       (DSL 로드 시) parseDslToIr → IR + coverage
```

### 4.2 Data Model Changes

DB 변경 없음(클라이언트 상태). 발행 시 서버 반영은 `lazyfga-7`.

### 4.3 Core Logic

캔버스 ↔ IR 매핑(결정적·양방향):
1. **노드:** 각 `ResourceType`/`GroupType` = 노드 1개. 노드 id = type name. 노드 종류(resource/group)로 구분 렌더.
2. **엣지:** `ResourceType.parents[]`의 각 항 = `child(resource) → parentTypes 각각` 방향 엣지. 엣지 생성 = 대상 resource에서 같은 `relationName`(기본 "parent")의 `ParentRef`가 있으면 그 `parentTypes`에 parent 타입을 추가(병합), 없으면 `ParentRef{relationName, parentTypes:[parent]}` 신규. 엣지 삭제 = 해당 parent 타입을 `parentTypes`에서 제거하고, 비면 ParentRef 제거(+ 참조하던 `permission.inheritFromParents` 정리).
3. **노드 추가:** 빈 `ResourceType{name, parents:[], roles:[], permissions:[]}` 또는 `GroupType{name, memberTypes:[{kind:"user"}]}` 삽입.
4. **노드 삭제:** 해당 type 제거 + 이를 parentType/group으로 참조하던 모든 ParentRef·SubjectRef 정리(고아 참조 금지 — `validateModelIR` 규칙과 일치).
5. **read-only 게이트:** `coverage.fullyRepresentable===false`이면 모든 변형 핸들러를 비활성화하고 DSL 텍스트를 원본으로 표시(CONCEPT 경계 정책).

상태 관리: `zustand` 스토어가 `ModelIR` 단일 보유. React Flow의 nodes/edges는 IR로부터 파생(selector). 사용자 조작 → IR 변형 액션 → 파생 재계산(단방향 데이터 흐름).

> 구현 노트(교차리뷰): DSL 재계산은 **동기**로 수행한다(디바운스 대신). 컴파일러가 isomorphic·경량이라 매 변형마다 즉시 컴파일해도 비용이 작고, 미리보기/검증이 항상 IR과 정합한다(더 강한 일관성). `validateModelIR` 통과 후에도 변환이 실패하면 `compileError`로 표면화한다.

## 5. API Design

### 5-1. New / Modified

```ts
// web/features/model-canvas/useModelGraph.ts
/** ModelIR ⇄ React Flow 노드/엣지 파생 및 변형 액션. */
export function useModelGraph(): {
  ir: ModelIR;
  nodes: RFNode[]; edges: RFEdge[];           // IR 파생(read)
  addResource(name: string): void;
  addGroup(name: string): void;
  removeType(name: string): void;             // 고아 참조 정리 포함
  connectParent(childType: string, parentType: string): void; // ParentRef 추가/병합(relationName 기준)
  disconnectParent(childType: string, relationName: string): void;
  readOnly: boolean;                          // coverage 기반
  dsl: string; errors: ValidationError[];     // 파생(compile/validate)
};
```

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| `validateModelIR` 위반 | 변형은 허용하되 `errors`로 인라인 표시(저장/발행은 `lazyfga-7`에서 차단) |
| `compileIrToDsl` throw | DSL 패널에 컴파일 에러 메시지, 마지막 정상 DSL 유지 |
| read-only 상태에서 변형 시도 | 무시(핸들러 비활성) |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                  | Estimated | Owner |
|---------|-------------------------------------------------------|-----------|-------|
| Phase 1 | zustand IR 스토어 + IR↔nodes/edges 파생                | 1d        | TBD   |
| Phase 2 | 노드/엣지 CRUD 액션 + 고아 참조 정리                   | 1.5d      | TBD   |
| Phase 3 | DSL 실시간 미리보기 + 에러 인라인 + read-only 게이트    | 1d        | TBD   |

### 6-2. Dependencies

- `@xyflow/react`(React Flow), `zustand`.
- `packages/compiler`(`compileIrToDsl`, `parseDslToIr`), `packages/shared`(`ModelIR`, `validateModelIR`).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §1 "노드로 그리는 모델 설계", §"비주얼이 표현하는 범위"
- [React Flow (xyflow)](https://reactflow.dev/)
- `lazyfga-2/3/4`
