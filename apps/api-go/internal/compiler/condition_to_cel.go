// Package compiler는 packages/compiler(백엔드 소비 부분집합)의 Go 포트다: ir-to-dsl(결정적
// DSL emitter + 공식 Go transformer)과 condition-to-cel. DSL/CEL 출력은 TS와 바이트 동일하며
// 이는 cross-language parity 코퍼스로 보증한다(LFGA-24). dsl-to-ir/coverage는 web 전용이라
// 포트하지 않는다.
package compiler

import (
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

var timeOp = map[string]string{"lt": "<", "lte": "<=", "gt": ">", "gte": ">="}
var valueOp = map[string]string{"eq": "==", "neq": "!=", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}

// celLiteral: string은 JSON.stringify, number/bool은 String(v) 재현.
func celLiteral(v contract.ConditionValue) string {
	switch v.Kind {
	case contract.ValueString:
		return jsutil.JSONString(v.Str)
	case contract.ValueNumber:
		return jsutil.NumberString(v.Num)
	case contract.ValueBool:
		if v.Bool {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func leafToCel(leaf *contract.ConditionLeaf) string {
	switch leaf.Kind {
	case "time":
		var rhs string
		if leaf.Rhs != nil && leaf.Rhs.Kind == "literal" {
			rhs = "timestamp(" + jsutil.JSONString(leaf.Rhs.RFC3339) + ")"
		} else if leaf.Rhs != nil {
			rhs = leaf.Rhs.Param
		}
		return leaf.Param + " " + timeOp[leaf.Op] + " " + rhs
	case "ip":
		return leaf.Param + ".in_cidr(" + jsutil.JSONString(leaf.Cidr) + ")"
	default: // "value"
		var v contract.ConditionValue
		if leaf.Value != nil {
			v = *leaf.Value
		}
		return leaf.Param + " " + valueOp[leaf.Op] + " " + celLiteral(v)
	}
}

// nodeToCel: top(=root)은 괄호 없이 평탄, 중첩 그룹만 괄호로 감싼다. single-child는 unwrap,
// 빈 그룹은 "true"(검증에서 막히지만 방어적).
func nodeToCel(node contract.ConditionNode, top bool) string {
	if node.Group == nil {
		return leafToCel(node.Leaf)
	}
	children := node.Group.Children
	if len(children) == 0 {
		return "true"
	}
	if len(children) == 1 {
		return nodeToCel(children[0], top)
	}
	sep := " && "
	if node.Group.Op == "or" {
		sep = " || "
	}
	parts := make([]string, len(children))
	for i, c := range children {
		parts[i] = nodeToCel(c, false)
	}
	inner := strings.Join(parts, sep)
	if top {
		return inner
	}
	return "(" + inner + ")"
}

// ConditionToCel은 조건 정의를 OpenFGA condition 선언 헤더(decl)와 CEL 본문(cel)으로 렌더한다.
func ConditionToCel(def *contract.ConditionDef) (decl string, cel string) {
	parts := make([]string, len(def.Params))
	for i, p := range def.Params {
		parts[i] = p.Name + ": " + p.Type
	}
	decl = "condition " + def.Name + "(" + strings.Join(parts, ", ") + ")"
	cel = nodeToCel(def.Tree, true)
	return decl, cel
}
