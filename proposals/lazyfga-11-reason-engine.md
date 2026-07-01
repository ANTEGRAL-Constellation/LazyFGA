# Reason Engine (Explainability) - Spec Proposal

| Item      | Detail                          |
| --------- | ------------------------------- |
| Author    | Seonguk Moon                    |
| Created   | 2026-06-28                      |
| Status    | **Implemented**                 |
| Reviewers | Claude, Codex (M4 cross-review) |

---

## 1. Summary

evaluate 결정에 대해 사람이 읽는 `reason`을 생성한다. 발행된 모델 IR과 표적 Check/Read를 사용해, **허용은 성립한 witnessing path**를, **거부는 best-effort "빠진 연결고리"**를 산출한다. (CONCEPT 모순 #4의 비대칭 정책을 그대로 구현)

## 2. Background & Motivation

- OpenFGA `Check`는 `allowed: bool`만 준다 → 이유는 직접 재구성해야 한다(메모리 `concept-gaps` #4).
- 허용/거부의 비대칭: 허용은 "경로가 있다"는 존재 증명(하나면 충분), 거부는 "경로가 없다"라 보여줄 게 없으므로 요구 조건을 짚는 best-effort.

## 3. Goals & Non-Goals

### 3.1 Goals

- [ ] `explain(user, permission, object, context): ReasonResult` 구현(IR 구조 기반 분해 + 표적 Check/Read).
- [ ] 허용: 하나의 witnessing path(역할/그룹/부모 상속 중 무엇이 권한을 줬는지).
- [ ] 거부: 권한을 줄 수 있었던 요구 조건 목록(역할/부모) = missing links.
- [ ] `lazyfga-9` evaluate에 `options.reason=true`일 때 응답 `context.reason`으로 부착.

### 3.2 Non-Goals

- [ ] 모든 가능한 경로 열거(허용은 1개 witness만). 전체 그래프 시각화 데이터는 `lazyfga-12`가 가공.
- [ ] 거부에 대한 "가장 가까운" 정교한 그래프 거리 계산 — MVP는 요구 조건 나열 + 단순 힌트까지.

## 4. Technical Design

### 4.1 Architecture Overview

```
explain(user, perm, object)
  IR(current) 로드 → can_<perm> = OR(grantedByRoles R_i) OR(from-parent P_j)
  허용 분해:
    for R_i: Check(user, R_i, object) → true면 witness=role R_i (+ 직접/그룹 판별: Read)
    for P_j: parent = Read(object, P_j) → recurse explain(user, perm, parent) (bounded depth)
  거부:
    missingLinks = { roles: R_i on object, parents: P_j 경유 can_<perm> }
```

### 4.2 Data Model Changes

변경 없음. 현재 모델 IR은 `model_version.ir_json`(`lazyfga-7`)에서 로드.

### 4.3 Core Logic

전제: `decision`은 `lazyfga-9`가 이미 계산(allowed bool). reason은 그 후 호출(추가 비용은 explain 요청 시에만).

**허용 경로 재구성(존재 증명, bounded):**

1. 현재 IR에서 `(resourceType=object.type)`의 `can_<perm>` 정의를 읽어 `grantedByRoles=[R_i]`, `inheritFromParents=[P_j]`를 얻는다.
2. 각 `R_i`에 대해 `Check(user, R_i, object)`. 최초 true인 `R_i`가 witness 역할.
   - 직접/그룹 판별: `Read(object, R_i)` tuple을 본다. user가 직접 주체면 `direct=true`. 직접이 아니고 group userset(`<group>#member`)이 있으면, 각 후보 group에 대해 `Check(user, "member", group)`로 **실제 멤버십을 확인**하고 참인 group만 path에 포함(여러 후보 중 확인된 것 선택). 한도 내 확인 실패 시 `group` 미지정 + `truncated=true`.
3. 2에서 못 찾으면 각 `P_j`에 대해 `parent = Read(object, relationName=P_j)`로 부모 객체를 얻고, `explain(user, perm, parent)`를 깊이 한도(기본 8) 내 재귀. 성립 시 path에 "inherited from parent" 단계 추가.
4. 어느 것도 못 찾았는데 decision=true인 경우(이론상 모델/데이터 경합) → path 없이 `text="allowed via can_<perm> (path 재구성 실패)"` 폴백(정직하게 한계 표기). 깊이 초과도 동일 폴백.

**거부 missing links(best-effort):**

1. IR에서 `can_<perm>` 요구 조건 추출: "object에 역할 [R_i] 중 하나" 또는 "부모 [P_j]에서 can_<perm>".
2. (선택 힌트) 각 `R_i`에 대해 user가 _다른_ 객체에서 `R_i`를 갖는지 가벼운 신호만 — MVP는 생략 가능(요구 조건 나열로 충분).
3. `text` 예: "거부: document:123 에 viewer/editor/owner 중 하나가 필요하거나, 부모 folder 에서 read 권한이 필요함."

**결정성/한계:** witness 탐색은 `grantedByRoles` 입력 순서를 따른다(결정적). 재귀 깊이·Read 페이지는 한도를 두고, 초과 시 폴백 텍스트로 정직하게 표기(과장 금지).

## 5. API Design

### 5-1. New / Modified

```ts
// packages/shared/src/reason.ts  — web·api 공유 타입(lazyfga-0 shared index에 ./reason 등록)
export interface ReasonResult {
  decision: boolean;
  path?: ReasonStep[]; // 허용일 때 witnessing path (없으면 폴백 text)
  missingLinks?: MissingLink[]; // 거부일 때
  text: string; // 사람이 읽는 한 줄
  truncated?: boolean; // 깊이/페이지 한도로 폴백되었는지
}
export type ReasonStep =
  | { via: "role"; role: string; on: string; direct: boolean; group?: string }
  | { via: "parent"; relation: string; parent: string };
export type MissingLink =
  | { kind: "role"; anyOf: string[]; on: string }
  | { kind: "parent"; relation: string; needs: string };

// apps/api/src/modules/pdp/reason.ts
/** decision 계산 후 reason 재구성. IR 구조 + 표적 Check/Read 사용. */
export function explain(
  user: string,
  permission: string,
  object: string,
  context?: Record<string, unknown>,
): Promise<ReasonResult>;
```

`lazyfga-9` evaluate 확장: 요청에 `options.reason=true`면 evaluate가 `explain(...)`를 호출해 `EvaluationResponse.context.reason = ReasonResult`로 반환. 기본(flag 없음)은 decision만(hot-path 경량 유지).

### 5-2. Error Handling

| 상황                      | 처리                                                        |
| ------------------------- | ----------------------------------------------------------- |
| 현재 모델 IR 부재(미발행) | `explain`은 `text="모델 미발행"` + decision 그대로          |
| Expand/Read 한도 초과     | 폴백 텍스트 + `truncated=true`(예외 아님)                   |
| OpenFGA Read 오류         | 500(상위 evaluate가 처리), reason 없이 decision 반환은 유지 |

## 6. Implementation Plan

### 6-1. Milestones

| Phase   | Task                                                 | Estimated | Owner |
| ------- | ---------------------------------------------------- | --------- | ----- |
| Phase 1 | IR 기반 요구 조건 추출 + 거부 missing links          | 1d        | TBD   |
| Phase 2 | 허용 witness 분해(role/그룹/parent 재귀, 한도)       | 1.5d      | TBD   |
| Phase 3 | evaluate `options.reason` 통합 + 테스트(픽스처 모델) | 0.5d      | TBD   |

### 6-2. Dependencies

- `apps/api/src/openfga`(`check`, `read`), `lazyfga-7`(현재 IR), `lazyfga-9`(evaluate 통합), `packages/shared`(`reason.ts` 타입).

## 7. References

- [CONCEPT.md](../CONCEPT.md) §4 explainability(허용/거부 비대칭)
- 메모리 `lazyfga-concept-logical-gaps` #4
- [OpenFGA Relationship Queries (Expand/Read)](https://openfga.dev/docs/interacting/relationship-queries)
