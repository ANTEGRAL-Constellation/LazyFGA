package model

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
)

// DiffChange는 두 IR의 구조 diff 1건이다. 종류별로 노출 필드가 다르며(판별 유니온),
// MarshalJSON이 TS 객체 리터럴과 동일한 필드 순서를 낸다. added/removed는 non-nil이면
// 빈 배열도 []로 직렬화한다(ROLE_ASSIGNABLE_CHANGED 등에서 한쪽이 비어도 [] 유지).
type DiffChange struct {
	Kind         string
	Type         string
	Role         string
	Permission   string
	Added        []string
	Removed      []string
	RelationName string
	ParentType   string
}

// marshalNoEscape는 encoding/json의 HTML 이스케이프를 끈 마샬이다(TS JSON.stringify와 바이트 parity).
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// MarshalJSON은 change 종류별 필드 순서를 TS diff.ts의 push 리터럴과 바이트 동일하게 낸다.
func (c DiffChange) MarshalJSON() ([]byte, error) {
	switch c.Kind {
	case "TYPE_ADDED", "TYPE_REMOVED":
		return marshalNoEscape(struct {
			Kind string `json:"kind"`
			Type string `json:"type"`
		}{c.Kind, c.Type})
	case "ROLE_ADDED", "ROLE_REMOVED":
		return marshalNoEscape(struct {
			Kind string `json:"kind"`
			Type string `json:"type"`
			Role string `json:"role"`
		}{c.Kind, c.Type, c.Role})
	case "ROLE_ASSIGNABLE_CHANGED":
		return marshalNoEscape(struct {
			Kind    string   `json:"kind"`
			Type    string   `json:"type"`
			Role    string   `json:"role"`
			Added   []string `json:"added"`
			Removed []string `json:"removed"`
		}{c.Kind, c.Type, c.Role, c.Added, c.Removed})
	case "PERMISSION_ADDED", "PERMISSION_REMOVED":
		return marshalNoEscape(struct {
			Kind       string `json:"kind"`
			Type       string `json:"type"`
			Permission string `json:"permission"`
		}{c.Kind, c.Type, c.Permission})
	case "GRANT_CHANGED", "PERMISSION_INHERIT_CHANGED":
		return marshalNoEscape(struct {
			Kind       string   `json:"kind"`
			Type       string   `json:"type"`
			Permission string   `json:"permission"`
			Added      []string `json:"added"`
			Removed    []string `json:"removed"`
		}{c.Kind, c.Type, c.Permission, c.Added, c.Removed})
	case "PARENT_ADDED", "PARENT_REMOVED":
		return marshalNoEscape(struct {
			Kind         string `json:"kind"`
			Type         string `json:"type"`
			RelationName string `json:"relationName"`
			ParentType   string `json:"parentType"`
		}{c.Kind, c.Type, c.RelationName, c.ParentType})
	default:
		return nil, errBadDiffKind
	}
}

var errBadDiffKind = errorString("model: invalid DiffChange kind")

type errorString string

func (e errorString) Error() string { return string(e) }

// sep는 식별자에 절대 나타날 수 없는 NUL 구분자다(parent pair 인코딩).
const sep = "\x00"

func subjectKey(ref contract.SubjectRef) string {
	if ref.Kind == "group" {
		return ref.Group + "#member"
	}
	return "user"
}

func subjectKeySet(role contract.Role) map[string]bool {
	s := make(map[string]bool, len(role.AssignableBy))
	for _, ref := range role.AssignableBy {
		s[subjectKey(ref)] = true
	}
	return s
}

func stringSet(xs []string) map[string]bool {
	s := make(map[string]bool, len(xs))
	for _, x := range xs {
		s[x] = true
	}
	return s
}

func parentPairs(r *contract.ResourceType) map[string]bool {
	s := make(map[string]bool)
	for _, p := range r.Parents {
		for _, pt := range p.ParentTypes {
			s[p.RelationName+sep+pt] = true
		}
	}
	return s
}

// setDiff는 from→to의 추가/제거를 정렬된 non-nil 슬라이스로 반환한다(빈 결과도 [] 유지).
func setDiff(from, to map[string]bool) (added, removed []string) {
	added = []string{}
	removed = []string{}
	for x := range to {
		if !from[x] {
			added = append(added, x)
		}
	}
	for x := range from {
		if !to[x] {
			removed = append(removed, x)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}

func typeNameSet(ir *contract.ModelIR) map[string]bool {
	s := make(map[string]bool, len(ir.Groups)+len(ir.Resources))
	for _, g := range ir.Groups {
		s[g.Name] = true
	}
	for _, r := range ir.Resources {
		s[r.Name] = true
	}
	return s
}

func resourceByName(ir *contract.ModelIR) map[string]*contract.ResourceType {
	m := make(map[string]*contract.ResourceType, len(ir.Resources))
	for i := range ir.Resources {
		m[ir.Resources[i].Name] = &ir.Resources[i]
	}
	return m
}

// DiffModels는 from→to의 구조 diff을 결정적으로(각 change의 JSON 바이트 정렬) 반환한다.
func DiffModels(from, to *contract.ModelIR) []DiffChange {
	changes := make([]DiffChange, 0)
	fromTypes := typeNameSet(from)
	toTypes := typeNameSet(to)

	for t := range toTypes {
		if !fromTypes[t] {
			changes = append(changes, DiffChange{Kind: "TYPE_ADDED", Type: t})
		}
	}
	for t := range fromTypes {
		if !toTypes[t] {
			changes = append(changes, DiffChange{Kind: "TYPE_REMOVED", Type: t})
		}
	}

	fromRes := resourceByName(from)
	toRes := resourceByName(to)

	for name, tr := range toRes {
		fr, ok := fromRes[name]
		if !ok {
			continue // 새 타입은 TYPE_ADDED로 이미 표기.
		}

		frRoles := roleByName(fr)
		trRoles := roleByName(tr)
		for rn, tRole := range trRoles {
			fRole, ok := frRoles[rn]
			if !ok {
				changes = append(changes, DiffChange{Kind: "ROLE_ADDED", Type: name, Role: rn})
				continue
			}
			added, removed := setDiff(subjectKeySet(*fRole), subjectKeySet(*tRole))
			if len(added) > 0 || len(removed) > 0 {
				changes = append(changes, DiffChange{Kind: "ROLE_ASSIGNABLE_CHANGED", Type: name, Role: rn, Added: added, Removed: removed})
			}
		}
		for rn := range frRoles {
			if _, ok := trRoles[rn]; !ok {
				changes = append(changes, DiffChange{Kind: "ROLE_REMOVED", Type: name, Role: rn})
			}
		}

		frPerms := permByName(fr)
		trPerms := permByName(tr)
		for pn, tp := range trPerms {
			fp, ok := frPerms[pn]
			if !ok {
				changes = append(changes, DiffChange{Kind: "PERMISSION_ADDED", Type: name, Permission: pn})
				continue
			}
			gAdded, gRemoved := setDiff(stringSet(fp.GrantedByRoles), stringSet(tp.GrantedByRoles))
			if len(gAdded) > 0 || len(gRemoved) > 0 {
				changes = append(changes, DiffChange{Kind: "GRANT_CHANGED", Type: name, Permission: pn, Added: gAdded, Removed: gRemoved})
			}
			iAdded, iRemoved := setDiff(stringSet(fp.InheritFromParents), stringSet(tp.InheritFromParents))
			if len(iAdded) > 0 || len(iRemoved) > 0 {
				changes = append(changes, DiffChange{Kind: "PERMISSION_INHERIT_CHANGED", Type: name, Permission: pn, Added: iAdded, Removed: iRemoved})
			}
		}
		for pn := range frPerms {
			if _, ok := trPerms[pn]; !ok {
				changes = append(changes, DiffChange{Kind: "PERMISSION_REMOVED", Type: name, Permission: pn})
			}
		}

		fp := parentPairs(fr)
		tp := parentPairs(tr)
		for pair := range tp {
			if !fp[pair] {
				rel, pt := splitPair(pair)
				changes = append(changes, DiffChange{Kind: "PARENT_ADDED", Type: name, RelationName: rel, ParentType: pt})
			}
		}
		for pair := range fp {
			if !tp[pair] {
				rel, pt := splitPair(pair)
				changes = append(changes, DiffChange{Kind: "PARENT_REMOVED", Type: name, RelationName: rel, ParentType: pt})
			}
		}
	}

	// 결정적·로케일 비의존 정렬: 각 change의 JSON 인코딩 바이트 비교(TS와 동일 순열).
	type keyed struct {
		c DiffChange
		k []byte
	}
	keys := make([]keyed, len(changes))
	for i, c := range changes {
		b, _ := c.MarshalJSON()
		keys[i] = keyed{c, b}
	}
	sort.SliceStable(keys, func(i, j int) bool { return bytes.Compare(keys[i].k, keys[j].k) < 0 })
	out := make([]DiffChange, len(keys))
	for i := range keys {
		out[i] = keys[i].c
	}
	return out
}

func roleByName(r *contract.ResourceType) map[string]*contract.Role {
	m := make(map[string]*contract.Role, len(r.Roles))
	for i := range r.Roles {
		m[r.Roles[i].Name] = &r.Roles[i]
	}
	return m
}

func permByName(r *contract.ResourceType) map[string]*contract.Permission {
	m := make(map[string]*contract.Permission, len(r.Permissions))
	for i := range r.Permissions {
		m[r.Permissions[i].Name] = &r.Permissions[i]
	}
	return m
}

func splitPair(pair string) (relationName, parentType string) {
	for i := 0; i < len(pair); i++ {
		if pair[i] == 0 {
			return pair[:i], pair[i+1:]
		}
	}
	return pair, ""
}
