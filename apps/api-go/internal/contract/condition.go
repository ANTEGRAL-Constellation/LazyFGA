package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"

	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

// 조건(ABAC/CEL) 트리 계약(LFGA-13/14). 판별 유니온(TimeRhs/ConditionLeaf/ConditionNode)과
// 값 리터럴(string|number|bool)은 커스텀 (un)marshaler로 TS와 바이트 호환 JSON을 유지한다.

// ConditionParamType: OpenFGA 네이티브 condition 파라미터 타입의 부분집합(MVP).
// "timestamp" | "ipaddress" | "string" | "int" | "double" | "bool".

// ConditionParam은 CEL 파라미터(name/type).
type ConditionParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ValueKind는 ConditionValue 판별자.
type ValueKind int

const (
	ValueString ValueKind = iota
	ValueNumber
	ValueBool
)

// ConditionValue는 value leaf의 RHS(string|number|bool). 어떤 JSON 타입이었는지 보존한다.
type ConditionValue struct {
	Kind ValueKind
	Str  string
	Num  float64
	Bool bool
}

// StringValue/NumberValue/BoolValue: 생성 헬퍼.
func StringValue(s string) ConditionValue  { return ConditionValue{Kind: ValueString, Str: s} }
func NumberValue(f float64) ConditionValue { return ConditionValue{Kind: ValueNumber, Num: f} }
func BoolValue(b bool) ConditionValue      { return ConditionValue{Kind: ValueBool, Bool: b} }

func (v ConditionValue) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case ValueString:
		// JS JSON.stringify와 바이트 동일(\b/\f 축약, <>&·U+2028/29 raw 포함) — jsutil이 단일 원본.
		return []byte(jsutil.JSONString(v.Str)), nil
	case ValueNumber:
		// JS 숫자 표기(-0→"0", 지수 무패딩, [1e-6,1e21) 십진). 비유한값은 JSON이 아니므로 오류.
		if math.IsNaN(v.Num) || math.IsInf(v.Num, 0) {
			return nil, errors.New("contract: condition value number must be finite")
		}
		return []byte(jsutil.NumberString(v.Num)), nil
	case ValueBool:
		if v.Bool {
			return []byte("true"), nil
		}
		return []byte("false"), nil
	default:
		return nil, errors.New("contract: invalid ConditionValue kind")
	}
}

func (v *ConditionValue) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("contract: condition value must be string|number|bool")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*v = StringValue(s)
	case 't', 'f':
		var x bool
		if err := json.Unmarshal(trimmed, &x); err != nil {
			return err
		}
		*v = BoolValue(x)
	default:
		var f float64
		if err := json.Unmarshal(trimmed, &f); err != nil {
			return err
		}
		*v = NumberValue(f)
	}
	return nil
}

// TimeRhs: 시간 비교 우변 — literal(rfc3339) 또는 param.
type TimeRhs struct {
	Kind    string // "literal" | "param"
	RFC3339 string // literal
	Param   string // param
}

func (r TimeRhs) MarshalJSON() ([]byte, error) {
	switch r.Kind {
	case "literal":
		return marshalNoEscape(struct {
			Kind    string `json:"kind"`
			RFC3339 string `json:"rfc3339"`
		}{r.Kind, r.RFC3339})
	case "param":
		return marshalNoEscape(struct {
			Kind  string `json:"kind"`
			Param string `json:"param"`
		}{r.Kind, r.Param})
	default:
		return nil, errors.New("contract: invalid TimeRhs kind")
	}
}

func (r *TimeRhs) UnmarshalJSON(b []byte) error {
	var probe struct {
		Kind    string `json:"kind"`
		RFC3339 string `json:"rfc3339"`
		Param   string `json:"param"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	switch probe.Kind {
	case "literal":
		*r = TimeRhs{Kind: "literal", RFC3339: probe.RFC3339}
	case "param":
		*r = TimeRhs{Kind: "param", Param: probe.Param}
	default:
		return errors.New("contract: unknown TimeRhs kind: " + probe.Kind)
	}
	return nil
}

// ConditionLeaf: 단일 비교 leaf(time|ip|value).
type ConditionLeaf struct {
	Kind  string // "time" | "ip" | "value"
	Param string
	Op    string
	Rhs   *TimeRhs        // time
	Cidr  string          // ip
	Value *ConditionValue // value
}

func (l ConditionLeaf) MarshalJSON() ([]byte, error) {
	switch l.Kind {
	case "time":
		return marshalNoEscape(struct {
			Kind  string  `json:"kind"`
			Param string  `json:"param"`
			Op    string  `json:"op"`
			Rhs   TimeRhs `json:"rhs"`
		}{l.Kind, l.Param, l.Op, deref(l.Rhs)})
	case "ip":
		return marshalNoEscape(struct {
			Kind  string `json:"kind"`
			Param string `json:"param"`
			Op    string `json:"op"`
			Cidr  string `json:"cidr"`
		}{l.Kind, l.Param, l.Op, l.Cidr})
	case "value":
		return marshalNoEscape(struct {
			Kind  string         `json:"kind"`
			Param string         `json:"param"`
			Op    string         `json:"op"`
			Value ConditionValue `json:"value"`
		}{l.Kind, l.Param, l.Op, derefValue(l.Value)})
	default:
		return nil, errors.New("contract: invalid ConditionLeaf kind")
	}
}

func (l *ConditionLeaf) UnmarshalJSON(b []byte) error {
	var probe struct {
		Kind  string          `json:"kind"`
		Param string          `json:"param"`
		Op    string          `json:"op"`
		Rhs   *TimeRhs        `json:"rhs"`
		Cidr  string          `json:"cidr"`
		Value *ConditionValue `json:"value"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	switch probe.Kind {
	case "time":
		*l = ConditionLeaf{Kind: "time", Param: probe.Param, Op: probe.Op, Rhs: probe.Rhs}
	case "ip":
		*l = ConditionLeaf{Kind: "ip", Param: probe.Param, Op: probe.Op, Cidr: probe.Cidr}
	case "value":
		*l = ConditionLeaf{Kind: "value", Param: probe.Param, Op: probe.Op, Value: probe.Value}
	default:
		return errors.New("contract: unknown ConditionLeaf kind: " + probe.Kind)
	}
	return nil
}

// ConditionGroup: AND/OR 그룹.
type ConditionGroup struct {
	Op       string          `json:"op"`
	Children []ConditionNode `json:"children"`
}

// ConditionNode: group 또는 leaf(둘 중 하나만 non-nil). 판별은 "children" 키 유무.
type ConditionNode struct {
	Group *ConditionGroup
	Leaf  *ConditionLeaf
}

// GroupNode/LeafNode: 생성 헬퍼.
func GroupNode(op string, children ...ConditionNode) ConditionNode {
	return ConditionNode{Group: &ConditionGroup{Op: op, Children: children}}
}
func LeafNode(l ConditionLeaf) ConditionNode { return ConditionNode{Leaf: &l} }

func (n ConditionNode) isGroup() bool { return n.Group != nil }

func (n ConditionNode) MarshalJSON() ([]byte, error) {
	if n.Group != nil {
		return marshalNoEscape(n.Group)
	}
	if n.Leaf != nil {
		return marshalNoEscape(*n.Leaf)
	}
	return nil, errors.New("contract: empty ConditionNode")
}

func (n *ConditionNode) UnmarshalJSON(b []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	// zod 순서 union 재현: group(op∈and|or + children 배열)이 성공하면 group, 실패하면 leaf 폴백.
	// children 키 존재만으로 group을 강제하지 않는다(잉여 children을 가진 유효 leaf 수용).
	if opRaw, hasOp := probe["op"]; hasOp {
		var op string
		if json.Unmarshal(opRaw, &op) == nil && (op == "and" || op == "or") {
			if _, hasChildren := probe["children"]; hasChildren {
				var g ConditionGroup
				if err := json.Unmarshal(b, &g); err == nil {
					*n = ConditionNode{Group: &g}
					return nil
				}
			}
		}
	}
	var l ConditionLeaf
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	*n = ConditionNode{Leaf: &l}
	return nil
}

// ConditionDef: 이름 붙은 재사용 조건(OpenFGA condition 블록 1개에 대응).
type ConditionDef struct {
	Name   string           `json:"name"`
	Params []ConditionParam `json:"params"`
	Tree   ConditionNode    `json:"tree"`
}

func deref(r *TimeRhs) TimeRhs {
	if r == nil {
		return TimeRhs{}
	}
	return *r
}

func derefValue(v *ConditionValue) ConditionValue {
	if v == nil {
		return ConditionValue{}
	}
	return *v
}
