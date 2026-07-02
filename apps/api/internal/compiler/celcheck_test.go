package compiler_test

import (
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/compiler"
	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/google/cel-go/cel"
	"github.com/stretchr/testify/require"
)

// LFGA-22 D5: 생성된 CEL(순수 subset: timestamp/string/int/double/bool 비교)을 cel-go로
// 컴파일·타입체크해 결과 타입이 bool 인지 확인한다(문자열 빌드보다 강한 보증). ipaddress/in_cidr
// leaf는 OpenFGA 커스텀 확장이라 plain cel-go가 모르므로 제외한다(코퍼스 문자열 + E2E가 커버).

func celType(paramType string) *cel.Type {
	switch paramType {
	case "timestamp":
		return cel.TimestampType
	case "string":
		return cel.StringType
	case "int":
		return cel.IntType
	case "double":
		return cel.DoubleType
	case "bool":
		return cel.BoolType
	default:
		return nil
	}
}

func lit(v contract.ConditionValue) *contract.ConditionValue { return &v }

func TestGeneratedCELCompilesToBool(t *testing.T) {
	// 순수 subset 조건들(잘 타입된 리터럴). double↔double, int↔int 로 맞춘다.
	pureParams := []contract.ConditionParam{
		{Name: "current_time", Type: "timestamp"},
		{Name: "expiry", Type: "timestamp"},
		{Name: "tier", Type: "string"},
		{Name: "level", Type: "int"},
		{Name: "score", Type: "double"},
		{Name: "flag", Type: "bool"},
	}
	trees := map[string]contract.ConditionNode{
		"time-param":    contract.LeafNode(contract.ConditionLeaf{Kind: "time", Param: "current_time", Op: "lt", Rhs: &contract.TimeRhs{Kind: "param", Param: "expiry"}}),
		"time-literal":  contract.LeafNode(contract.ConditionLeaf{Kind: "time", Param: "current_time", Op: "gte", Rhs: &contract.TimeRhs{Kind: "literal", RFC3339: "2030-01-01T00:00:00Z"}}),
		"string-eq":     contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "tier", Op: "eq", Value: lit(contract.StringValue("gold"))}),
		"int-order":     contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "level", Op: "gte", Value: lit(contract.NumberValue(3))}),
		"double-order":  contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "score", Op: "gt", Value: lit(contract.NumberValue(1.5))}),
		"bool-eq":       contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "flag", Op: "eq", Value: lit(contract.BoolValue(true))}),
		"string-escape": contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "tier", Op: "neq", Value: lit(contract.StringValue(`a"b<c>&d`))}),
		"and-group": contract.GroupNode("and",
			contract.LeafNode(contract.ConditionLeaf{Kind: "time", Param: "current_time", Op: "lt", Rhs: &contract.TimeRhs{Kind: "param", Param: "expiry"}}),
			contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "tier", Op: "eq", Value: lit(contract.StringValue("gold"))}),
			contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "level", Op: "gte", Value: lit(contract.NumberValue(2))}),
		),
		"nested-or": contract.GroupNode("or",
			contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "tier", Op: "eq", Value: lit(contract.StringValue("gold"))}),
			contract.GroupNode("and",
				contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "score", Op: "gt", Value: lit(contract.NumberValue(0.5))}),
				contract.LeafNode(contract.ConditionLeaf{Kind: "value", Param: "score", Op: "lt", Value: lit(contract.NumberValue(9.5))}),
			),
		),
	}

	opts := make([]cel.EnvOption, 0, len(pureParams))
	for _, p := range pureParams {
		opts = append(opts, cel.Variable(p.Name, celType(p.Type)))
	}
	env, err := cel.NewEnv(opts...)
	require.NoError(t, err)

	for name, tree := range trees {
		tree := tree
		t.Run(name, func(t *testing.T) {
			def := &contract.ConditionDef{Name: "c", Params: pureParams, Tree: tree}
			// 먼저 우리 정적 검증을 통과해야 한다(잘못된 케이스로 cel-go를 속이지 않도록).
			require.Empty(t, contract.ValidateConditionDef(def), "def must be valid")
			_, celBody := compiler.ConditionToCel(def)

			ast, iss := env.Compile(celBody)
			require.NoError(t, iss.Err(), "cel compile %q", celBody)
			require.Equal(t, cel.BoolType, ast.OutputType(), "cel %q should be bool", celBody)
		})
	}
}
