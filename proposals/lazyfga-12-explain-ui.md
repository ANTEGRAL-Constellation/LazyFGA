# Explain UI (결정 경로 시각화) - Spec Proposal

| Item       | Detail                           |
|------------|----------------------------------|
| Author     | Seonguk Moon                     |
| Created    | 2026-06-28                       |
| Status     | **Implemented**                  |
| Reviewers  | Claude, Codex (M4 cross-review)  |

---

## 1. Summary

`reason-engine`(`lazyfga-11`)이 만든 `ReasonResult`를 web에서 시각화한다. 허용은 witnessing path를 그래프(캔버스의 타입 그래프 위)로 강조하고, 거부는 missing links를 표시한다. playground(`lazyfga-18`)와 결합해 "왜?"를 즉시 본다.

## 2. Background & Motivation

- CONCEPT 두 번째 차별 기둥(explainability). 인가 디버깅을 "왜 막혔지"에서 "이 연결만 있었으면"으로 바꾼다.
- reason은 텍스트만으로도 유용하지만, 그래프 경로 강조가 학습·납득에 결정적.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] evaluate(explain 모드) 호출 폼: subject/action/resource/context 입력 → decision + reason 표시.
- [ ] 허용: `ReasonStep[]`를 단계 카드 + 캔버스 위 경로 하이라이트(노드/엣지 강조)로 표현.
- [ ] 거부: `MissingLink[]`를 "필요 조건" 카드로 표시(어떤 역할/부모면 통과하는지).
- [ ] `truncated=true`면 "경로 일부만 재구성됨" 배지로 정직하게 표기.

### 3.2 Non-Goals

- [ ] reason 계산 로직(서버 `lazyfga-11`). 본 기능은 표현만.
- [ ] tuple 편집/시뮬레이션(후속).

## 4. Technical Design

### 4.1 Architecture Overview

```
web/features/explain
  ExplainForm(subject, action, resource, context)
     → POST /access/v1/evaluation { ..., options:{ reason:true } }  (service/admin 토큰)
     → ReasonResult
  ResultView:
     decision 배지(allow/deny)
     allow → PathView(ReasonStep[]) + CanvasHighlight(경로)
     deny  → MissingLinkView(MissingLink[])
     truncated → 경고 배지
```

### 4.2 Data Model Changes

없음(표현 전용).

### 4.3 Core Logic

- 입력 폼은 AuthZEN evaluate 요청 형태(`lazyfga-9`)를 그대로 구성하고 `options.reason=true`를 붙인다.
- `ReasonStep` → 캔버스 매핑:
  - `{via:"role", on, role, group?}` → `on` 타입 노드 강조 + 해당 role 배지; `group` 있으면 그 group 노드도 경로에 포함.
  - `{via:"parent", relation, parent}` → child→parent 상속 엣지 강조 + parent 노드 강조.
  - 경로는 순서대로 단계 카드로도 나열(그래프와 카드가 동일 데이터에서 파생).
- `MissingLink` → "필요" 카드: `{kind:"role", anyOf, on}` = "on 에 anyOf 중 하나 필요", `{kind:"parent", relation, needs}` = "부모(relation)에서 needs 필요".
- 모델이 read-only(coverage=false)여도 explain은 가능(읽기 전용 시각화).

## 5. API Design

### 5-1. New / Modified

신규 REST 없음(서버는 `lazyfga-9` evaluate + `lazyfga-11` reason 사용). web 컴포넌트 계약:

```ts
// web/features/explain
export function useExplain(): {
  run(req: EvaluationRequest): Promise<void>;   // options.reason=true 강제
  result?: ReasonResult;                         // packages/shared/reason.ts
  highlight: { nodes: string[]; edges: string[] }; // 캔버스 강조 대상(ReasonStep 파생)
  loading: boolean; error?: string;
};
```

> 참고: `ReasonResult`/`ReasonStep`/`MissingLink`는 `packages/shared/src/reason.ts`(lazyfga-11에서 정의)에서 가져온다 — web·api 공유.

### 5-2. Error Handling

| 상황 | 처리 |
|------|------|
| evaluate 401/500 | 결과 영역에 에러 표시, 캔버스 강조 없음 |
| reason 없음(decision만 옴) | 텍스트만 표시, 경로 강조 생략 |
| highlight 대상 노드 부재(모델 변경) | 해당 강조 스킵(깨지지 않게) |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                          | Estimated | Owner |
|---------|-----------------------------------------------|-----------|-------|
| Phase 1 | ExplainForm + evaluate(reason) 호출 + 결과 표시 | 1d        | TBD   |
| Phase 2 | allow PathView + 캔버스 경로 하이라이트          | 1.5d      | TBD   |
| Phase 3 | deny MissingLinkView + truncated 배지           | 0.5d      | TBD   |

### 6-2. Dependencies

- `lazyfga-5`(캔버스 강조 API), `lazyfga-9`(evaluate), `lazyfga-11`(ReasonResult; shared로 승격), `packages/shared`.

## 7. References

- [CONCEPT.md](../CONCEPT.md) §4 explainability
- `lazyfga-11`(reason-engine)
