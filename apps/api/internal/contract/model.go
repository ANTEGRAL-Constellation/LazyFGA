package contract

import (
	"encoding/json"
	"errors"
)

// 5-primitive ModelIR(Resource·Role·Permission·Hierarchy·Group). 각 필드는 OpenFGA 구문과
// 1:1 대응한다. SubjectRef만 판별 유니온이라 커스텀 (un)marshaler를 가진다.

// SubjectRef: 직접 user 또는 group member userset. condition이 있으면 type restriction에
// `with <condition>`이 붙는다(LFGA-14).
type SubjectRef struct {
	Kind      string  // "user" | "group"
	Group     string  // group
	Relation  string  // group, 항상 "member"
	Condition *string // optional
}

// UserRef/GroupRef: 생성 헬퍼.
func UserRef(condition *string) SubjectRef {
	return SubjectRef{Kind: "user", Condition: condition}
}
func GroupRef(group, relation string, condition *string) SubjectRef {
	return SubjectRef{Kind: "group", Group: group, Relation: relation, Condition: condition}
}

func (s SubjectRef) MarshalJSON() ([]byte, error) {
	switch s.Kind {
	case "user":
		return marshalNoEscape(struct {
			Kind      string  `json:"kind"`
			Condition *string `json:"condition,omitempty"`
		}{s.Kind, s.Condition})
	case "group":
		return marshalNoEscape(struct {
			Kind      string  `json:"kind"`
			Group     string  `json:"group"`
			Relation  string  `json:"relation"`
			Condition *string `json:"condition,omitempty"`
		}{s.Kind, s.Group, s.Relation, s.Condition})
	default:
		return nil, errors.New("contract: invalid SubjectRef kind")
	}
}

func (s *SubjectRef) UnmarshalJSON(b []byte) error {
	var probe struct {
		Kind      string  `json:"kind"`
		Group     string  `json:"group"`
		Relation  string  `json:"relation"`
		Condition *string `json:"condition"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	switch probe.Kind {
	case "user":
		*s = SubjectRef{Kind: "user", Condition: probe.Condition}
	case "group":
		*s = SubjectRef{Kind: "group", Group: probe.Group, Relation: probe.Relation, Condition: probe.Condition}
	default:
		return errors.New("contract: unknown SubjectRef kind: " + probe.Kind)
	}
	return nil
}

// GroupType: 주체 그룹. DSL: type <name> { relations { define member: [<memberTypes>] } }.
type GroupType struct {
	Name        string       `json:"name"`
	MemberTypes []SubjectRef `json:"memberTypes"`
}

// ParentRef: 상속 엣지. 같은 relationName은 단일 ParentRef로 병합.
type ParentRef struct {
	RelationName string   `json:"relationName"`
	ParentTypes  []string `json:"parentTypes"`
}

// Role: 부여 가능한 역할.
type Role struct {
	Name         string       `json:"name"`
	AssignableBy []SubjectRef `json:"assignableBy"`
}

// Permission: 검사용 권한(관계 이름 can_<name>).
type Permission struct {
	Name               string   `json:"name"`
	GrantedByRoles     []string `json:"grantedByRoles"`
	InheritFromParents []string `json:"inheritFromParents"`
}

// ResourceType.
type ResourceType struct {
	Name        string       `json:"name"`
	Parents     []ParentRef  `json:"parents"`
	Roles       []Role       `json:"roles"`
	Permissions []Permission `json:"permissions"`
}

// ModelIR: 5-primitive 단일 계약. conditions는 없으면 생략(LFGA-14).
type ModelIR struct {
	SchemaVersion string         `json:"schemaVersion"`
	Groups        []GroupType    `json:"groups"`
	Resources     []ResourceType `json:"resources"`
	Conditions    []ConditionDef `json:"conditions,omitempty"`
}
