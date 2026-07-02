package democli

import "encoding/json"

// 데모 IR 편집 헬퍼. shared/edit.ts의 addCondition / setAssignmentCondition 의미를 그대로 옮긴다
// (LFGA-22 §4.3: 이 두 연산만 백엔드에서 재구현, 나머지 edit.ts는 web 전용). IR은 순수 JSON이라
// map[string]any 위에서 동작하며, 각 연산은 입력을 변형하지 않고 새 IR을 반환한다(edit.ts와 동일).

// cloneJSON은 JSON 왕복 deep clone이다(edit.ts의 clone = JSON.parse(JSON.stringify(v)) 대응).
func cloneJSON(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// asSlice / asMap은 any를 []any / map[string]any로 좁힌다(부재/타입불일치면 zero, false).
func asSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// addCondition은 최상위 조건 정의를 추가한다(이름 중복이면 no-op) — edit.ts addCondition 포팅.
func addCondition(ir map[string]any, def map[string]any) map[string]any {
	conds, _ := asSlice(ir["conditions"])
	for _, c := range conds {
		if cm, ok := asMap(c); ok {
			if name, _ := cm["name"].(string); name == def["name"] {
				return ir // 중복 → no-op.
			}
		}
	}
	next, _ := asMap(cloneJSON(ir))
	nextConds, _ := asSlice(next["conditions"])
	nextConds = append(nextConds, cloneJSON(def))
	next["conditions"] = nextConds
	return next
}

// setAssignmentCondition은 역할 부여(assignableBy[subjectIndex])에 조건을 부착/해제한다
// (대상/범위 밖이면 no-op) — edit.ts setAssignmentCondition 포팅. condition==nil이면 해제.
func setAssignmentCondition(ir map[string]any, typeName, role string, subjectIndex int, condition *string) map[string]any {
	target := findRole(ir, typeName, role)
	if target == nil {
		return ir
	}
	assignableBy, _ := asSlice(target["assignableBy"])
	if subjectIndex < 0 || subjectIndex >= len(assignableBy) {
		return ir
	}
	next, _ := asMap(cloneJSON(ir))
	ref, ok := asMap(findRole(next, typeName, role)["assignableBy"].([]any)[subjectIndex])
	if !ok {
		return ir
	}
	if condition == nil {
		delete(ref, "condition")
	} else {
		ref["condition"] = *condition
	}
	return next
}

// findRole은 resources[typeName].roles[role] map을 찾는다(없으면 nil).
func findRole(ir map[string]any, typeName, role string) map[string]any {
	resources, _ := asSlice(ir["resources"])
	for _, r := range resources {
		rm, ok := asMap(r)
		if !ok {
			continue
		}
		if name, _ := rm["name"].(string); name != typeName {
			continue
		}
		roles, _ := asSlice(rm["roles"])
		for _, rl := range roles {
			rlm, ok := asMap(rl)
			if !ok {
				continue
			}
			if rn, _ := rlm["name"].(string); rn == role {
				return rlm
			}
		}
	}
	return nil
}

// demoIR은 임베드 fixture에 두 편집을 적용한 데모 모델 IR을 만든다.
// (1) non_expired 조건 추가, (2) document.owner.assignableBy[0]에 그 조건 부착.
func demoIR() (map[string]any, error) {
	var ir map[string]any
	if err := json.Unmarshal(docFolderTeamIR, &ir); err != nil {
		return nil, err
	}
	nonExpired := map[string]any{
		"name": "non_expired",
		"params": []any{
			map[string]any{"name": "current_time", "type": "timestamp"},
			map[string]any{"name": "expiry", "type": "timestamp"},
		},
		"tree": map[string]any{
			"kind":  "time",
			"param": "current_time",
			"op":    "lt",
			"rhs":   map[string]any{"kind": "param", "param": "expiry"},
		},
	}
	ir = addCondition(ir, nonExpired)
	cond := "non_expired"
	ir = setAssignmentCondition(ir, "document", "owner", 0, &cond)
	return ir, nil
}
