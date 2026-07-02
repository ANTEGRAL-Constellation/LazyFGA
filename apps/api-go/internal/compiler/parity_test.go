package compiler_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/compiler"
	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cross-language parity 코퍼스의 compiler 측 검증(LFGA-24): DSL emit + CEL emit이 TS 레퍼런스와
// 바이트 동일한지 확인한다. 코퍼스는 packages/shared 에 있고 실행 위치와 무관하게 로드한다.

func loadCorpus(t *testing.T, name string, v any) {
	t.Helper()
	p := testutil.RepoPath("packages", "shared", "src", "__fixtures__", "parity", name)
	data, err := os.ReadFile(p)
	require.NoError(t, err, "read %s", p)
	require.NoError(t, json.Unmarshal(data, v), "unmarshal %s", name)
}

func TestParityDSLCases(t *testing.T) {
	var corpus struct {
		Cases []struct {
			Name string           `json:"name"`
			IR   contract.ModelIR `json:"ir"`
			DSL  string           `json:"dsl"`
		} `json:"cases"`
	}
	loadCorpus(t, "dsl-cases.json", &corpus)
	require.NotEmpty(t, corpus.Cases)

	for _, c := range corpus.Cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			dsl, modelJSON, err := compiler.CompileIRToDSL(&c.IR)
			require.NoError(t, err)
			assert.Equal(t, c.DSL, dsl)
			assert.NotEmpty(t, modelJSON, "transformer produced model JSON")
		})
	}
}

func TestParityCELCases(t *testing.T) {
	var corpus struct {
		Cel []struct {
			Name string                `json:"name"`
			Def  contract.ConditionDef `json:"def"`
			Decl string                `json:"decl"`
			Cel  string                `json:"cel"`
		} `json:"cel"`
	}
	loadCorpus(t, "condition-cases.json", &corpus)
	require.NotEmpty(t, corpus.Cel)

	for _, c := range corpus.Cel {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			decl, cel := compiler.ConditionToCel(&c.Def)
			assert.Equal(t, c.Decl, decl)
			assert.Equal(t, c.Cel, cel)
		})
	}
}
