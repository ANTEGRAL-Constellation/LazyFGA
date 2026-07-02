package model

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
	fga "github.com/openfga/go-sdk"
	"github.com/stretchr/testify/require"
)

// 공식 transformer JSON → SDK 구조체 왕복이 필드를 잃지 않는지 corpus 전체로 고정한다
// (LFGA-26 리뷰 #14: SDK 버전 드리프트가 조용히 필드를 떨구면 OpenFGA에 기록되는 모델이
// 무경고로 달라질 수 있다 — 여기서 잡는다).
func TestTransformerJSONRoundTripsThroughSDK(t *testing.T) {
	var corpus struct {
		Cases []struct {
			Name string          `json:"name"`
			IR   json.RawMessage `json:"ir"`
		} `json:"cases"`
	}
	data, err := os.ReadFile(testutil.RepoPath("packages", "shared", "src", "__fixtures__", "parity", "dsl-cases.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &corpus))
	require.NotEmpty(t, corpus.Cases)

	comp := DefaultCompiler()
	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ir, issues := contract.DecodeModelIR(c.IR)
			require.Empty(t, issues)
			_, modelJSON, err := comp.Compile(ir)
			require.NoError(t, err)

			var req fga.WriteAuthorizationModelRequest
			require.NoError(t, json.Unmarshal(modelJSON, &req))
			remarshaled, err := json.Marshal(req)
			require.NoError(t, err)
			// 필드 집합/값 동등성(JSON 의미 비교) — 순서는 무관, 누락은 실패.
			require.JSONEq(t, string(modelJSON), string(remarshaled), "SDK round-trip dropped fields for %s", c.Name)
		})
	}
}
