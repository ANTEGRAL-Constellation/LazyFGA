package compiler

import (
	"errors"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
)

// 코퍼스가 닿지 않는 compiler 분기(에러 경로/방어적 분기)를 좁게 커버한다.

func TestCompileErrorMessage(t *testing.T) {
	e := &CompileError{Reason: "JSON_TRANSFORM_FAILED", Detail: errors.New("x")}
	if e.Error() != "compileIrToDsl failed: JSON_TRANSFORM_FAILED" {
		t.Fatalf("Error() = %q", e.Error())
	}
}

func TestCompileIRToDSLInvalidIR(t *testing.T) {
	// grantedByRoles 비어있음 → ValidateModelIR 위반 → IR_INVALID.
	ir := &contract.ModelIR{
		SchemaVersion: "1.1",
		Resources: []contract.ResourceType{{
			Name:        "doc",
			Roles:       []contract.Role{{Name: "viewer", AssignableBy: []contract.SubjectRef{{Kind: "user"}}}},
			Permissions: []contract.Permission{{Name: "read", GrantedByRoles: nil, InheritFromParents: nil}},
		}},
	}
	_, _, err := CompileIRToDSL(ir)
	var ce *CompileError
	if !errors.As(err, &ce) || ce.Reason != "IR_INVALID" {
		t.Fatalf("err = %v, want IR_INVALID CompileError", err)
	}
}

func TestConditionToCelEmptyGroupDefensive(t *testing.T) {
	// 빈 그룹은 검증에서 막히지만 emit은 방어적으로 "true".
	def := &contract.ConditionDef{Name: "c", Tree: contract.GroupNode("and")}
	_, cel := ConditionToCel(def)
	if cel != "true" {
		t.Fatalf("empty group cel = %q, want true", cel)
	}
}

func TestConditionToCelInvalidValueKindDefensive(t *testing.T) {
	// 잘못된 ValueKind → celLiteral default 분기("").
	bad := contract.ConditionValue{Kind: contract.ValueKind(99)}
	def := &contract.ConditionDef{
		Name: "c",
		Tree: contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "x", Op: "eq", Value: &bad}),
	}
	_, cel := ConditionToCel(def)
	if cel != "x == " {
		t.Fatalf("invalid value kind cel = %q, want %q", cel, "x == ")
	}
}

func TestConditionToCelSingleChildUnwrapAtTop(t *testing.T) {
	// top single-child 그룹 → unwrap.
	def := &contract.ConditionDef{
		Name:   "c",
		Params: []contract.ConditionParam{{Name: "tier", Type: "string"}},
		Tree:   contract.GroupNode("and", contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "tier", Op: "eq", Value: ptrVal(contract.StringValue("gold"))})),
	}
	_, cel := ConditionToCel(def)
	if cel != `tier == "gold"` {
		t.Fatalf("cel = %q", cel)
	}
}

func ptrVal(v contract.ConditionValue) *contract.ConditionValue { return &v }
