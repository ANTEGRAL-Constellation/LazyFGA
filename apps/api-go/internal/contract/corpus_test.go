package contract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cross-language parity 코퍼스의 contract 측 검증(LFGA-24): 검증기(model/condition/grant),
// strict shape 디코더, 그리고 커스텀 marshaler round-trip이 TS 레퍼런스와 동일한지 확인한다.

func loadCorpus(t *testing.T, name string, v any) {
	t.Helper()
	p := testutil.RepoPath("packages", "shared", "src", "__fixtures__", "parity", name)
	data, err := os.ReadFile(p)
	require.NoError(t, err, "read %s", p)
	require.NoError(t, json.Unmarshal(data, v), "unmarshal %s", name)
}

func assertValidationEqual(t *testing.T, want, got []contract.ValidationError) {
	t.Helper()
	if len(want) == 0 {
		assert.Empty(t, got)
		return
	}
	assert.Equal(t, want, got)
}

func assertConditionEqual(t *testing.T, want, got []contract.ConditionError) {
	t.Helper()
	if len(want) == 0 {
		assert.Empty(t, got)
		return
	}
	assert.Equal(t, want, got)
}

type marshalCase struct {
	Name string          `json:"name"`
	Type string          `json:"type"`
	JSON json.RawMessage `json:"json"`
}

func (c marshalCase) canonical(t *testing.T) string {
	var s string
	require.NoError(t, json.Unmarshal(c.JSON, &s), "unwrap canonical for %s", c.Name)
	return s
}

func TestCorpusModelCases(t *testing.T) {
	var corpus struct {
		Validate []struct {
			Name   string                     `json:"name"`
			IR     contract.ModelIR           `json:"ir"`
			Errors []contract.ValidationError `json:"errors"`
		} `json:"validate"`
		Shape []struct {
			Name  string          `json:"name"`
			JSON  json.RawMessage `json:"json"`
			Valid bool            `json:"valid"`
		} `json:"shape"`
		Marshal []marshalCase `json:"marshal"`
	}
	loadCorpus(t, "model-cases.json", &corpus)
	require.NotEmpty(t, corpus.Validate)

	for _, c := range corpus.Validate {
		c := c
		t.Run("validate/"+c.Name, func(t *testing.T) {
			assertValidationEqual(t, c.Errors, contract.ValidateModelIR(&c.IR))
		})
	}
	for _, c := range corpus.Shape {
		c := c
		t.Run("shape/"+c.Name, func(t *testing.T) {
			ir, issues := contract.DecodeModelIR(c.JSON)
			assert.Equal(t, c.Valid, len(issues) == 0, "issues=%v", issues)
			if c.Valid {
				assert.NotNil(t, ir)
			} else {
				assert.Nil(t, ir)
			}
		})
	}
	runMarshalCases(t, corpus.Marshal)
}

func TestCorpusConditionCases(t *testing.T) {
	var corpus struct {
		Validate []struct {
			Name   string                    `json:"name"`
			Def    contract.ConditionDef     `json:"def"`
			Errors []contract.ConditionError `json:"errors"`
		} `json:"validate"`
		Marshal []marshalCase `json:"marshal"`
	}
	loadCorpus(t, "condition-cases.json", &corpus)
	require.NotEmpty(t, corpus.Validate)

	for _, c := range corpus.Validate {
		c := c
		t.Run("validate/"+c.Name, func(t *testing.T) {
			assertConditionEqual(t, c.Errors, contract.ValidateConditionDef(&c.Def))
		})
	}
	runMarshalCases(t, corpus.Marshal)
}

func TestCorpusGrantCases(t *testing.T) {
	var corpus struct {
		Models        map[string]contract.ModelIR `json:"models"`
		ValidateGrant []struct {
			Name    string                `json:"name"`
			Model   string                `json:"model"`
			Req     contract.GrantRequest `json:"req"`
			OK      bool                  `json:"ok"`
			Code    string                `json:"code"`
			Message string                `json:"message"`
		} `json:"validateGrant"`
		ValidateRevoke []struct {
			Name    string                 `json:"name"`
			Model   string                 `json:"model"`
			Req     contract.RevokeRequest `json:"req"`
			OK      bool                   `json:"ok"`
			Code    string                 `json:"code"`
			Message string                 `json:"message"`
		} `json:"validateRevoke"`
		IsAssignable []struct {
			Name     string `json:"name"`
			Model    string `json:"model"`
			Type     string `json:"type"`
			Relation string `json:"relation"`
			Expect   bool   `json:"expect"`
		} `json:"isAssignable"`
		SubjectToUser []struct {
			Name    string                `json:"name"`
			Subject contract.GrantSubject `json:"subject"`
			Expect  string                `json:"expect"`
		} `json:"subjectToUser"`
		GrantTupleKey []struct {
			Name   string                `json:"name"`
			Req    contract.GrantRequest `json:"req"`
			Expect json.RawMessage       `json:"expect"`
		} `json:"grantTupleKey"`
		RevokeTupleKey []struct {
			Name   string                 `json:"name"`
			Req    contract.RevokeRequest `json:"req"`
			Expect json.RawMessage        `json:"expect"`
		} `json:"revokeTupleKey"`
		ParseResourceRef []struct {
			Name   string          `json:"name"`
			Input  string          `json:"input"`
			Expect json.RawMessage `json:"expect"`
		} `json:"parseResourceRef"`
		ParseGrantSubject []struct {
			Name   string          `json:"name"`
			Input  string          `json:"input"`
			Expect json.RawMessage `json:"expect"`
		} `json:"parseGrantSubject"`
		Marshal []marshalCase `json:"marshal"`
	}
	loadCorpus(t, "grant-cases.json", &corpus)

	model := func(name string) *contract.ModelIR {
		// 오타/누락 모델 키가 zero-value IR로 조용히 검증되는 것을 방지(TS 측은 undefined로 throw).
		m, ok := corpus.Models[name]
		if !ok {
			t.Fatalf("corpus model %q not found in grant-cases.json", name)
		}
		return &m
	}

	for _, c := range corpus.ValidateGrant {
		c := c
		t.Run("validateGrant/"+c.Name, func(t *testing.T) {
			r := contract.ValidateGrant(model(c.Model), &c.Req)
			assert.Equal(t, c.OK, r.OK)
			assert.Equal(t, c.Code, r.Code)
			assert.Equal(t, c.Message, r.Message)
		})
	}
	for _, c := range corpus.ValidateRevoke {
		c := c
		t.Run("validateRevoke/"+c.Name, func(t *testing.T) {
			r := contract.ValidateRevoke(model(c.Model), &c.Req)
			assert.Equal(t, c.OK, r.OK)
			assert.Equal(t, c.Code, r.Code)
			assert.Equal(t, c.Message, r.Message)
		})
	}
	for _, c := range corpus.IsAssignable {
		c := c
		t.Run("isAssignable/"+c.Name, func(t *testing.T) {
			assert.Equal(t, c.Expect, contract.IsAssignableRelation(model(c.Model), c.Type, c.Relation))
		})
	}
	for _, c := range corpus.SubjectToUser {
		c := c
		t.Run("subjectToUser/"+c.Name, func(t *testing.T) {
			assert.Equal(t, c.Expect, contract.SubjectToUser(c.Subject))
		})
	}
	for _, c := range corpus.GrantTupleKey {
		c := c
		t.Run("grantTupleKey/"+c.Name, func(t *testing.T) {
			got, err := json.Marshal(contract.GrantTupleKeyOf(&c.Req))
			require.NoError(t, err)
			assert.JSONEq(t, string(c.Expect), string(got))
		})
	}
	for _, c := range corpus.RevokeTupleKey {
		c := c
		t.Run("revokeTupleKey/"+c.Name, func(t *testing.T) {
			got, err := json.Marshal(contract.RevokeTupleKeyOf(&c.Req))
			require.NoError(t, err)
			assert.JSONEq(t, string(c.Expect), string(got))
		})
	}
	for _, c := range corpus.ParseResourceRef {
		c := c
		t.Run("parseResourceRef/"+c.Name, func(t *testing.T) {
			ref, ok := contract.ParseResourceRef(c.Input)
			assertParse(t, c.Expect, ok, ref)
		})
	}
	for _, c := range corpus.ParseGrantSubject {
		c := c
		t.Run("parseGrantSubject/"+c.Name, func(t *testing.T) {
			sub, ok := contract.ParseGrantSubject(c.Input)
			assertParse(t, c.Expect, ok, sub)
		})
	}
	runMarshalCases(t, corpus.Marshal)
}

func assertParse(t *testing.T, expect json.RawMessage, ok bool, got any) {
	t.Helper()
	if string(expect) == "null" {
		assert.False(t, ok)
		return
	}
	require.True(t, ok)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(expect), string(gotJSON))
}

func runMarshalCases(t *testing.T, cases []marshalCase) {
	require.NotEmpty(t, cases)
	for _, c := range cases {
		c := c
		t.Run("marshal/"+c.Type+"/"+c.Name, func(t *testing.T) {
			canonical := c.canonical(t)
			assert.Equal(t, canonical, roundTrip(t, c.Type, canonical))
		})
	}
}

func roundTrip(t *testing.T, typ, canonical string) string {
	t.Helper()
	raw := []byte(canonical)
	// 프로덕션 응답 경로(httpx.WriteJSON)와 동일하게 HTML 이스케이프를 끈 인코더를 쓴다.
	// json.Marshal은 커스텀 MarshalJSON 출력의 <>&·U+2028/29까지 재이스케이프해 TS와 어긋난다.
	marshal := func(v any) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		require.NoError(t, enc.Encode(v))
		return strings.TrimSuffix(buf.String(), "\n")
	}
	unmarshal := func(v any) { require.NoError(t, json.Unmarshal(raw, v), "unmarshal %s", typ) }
	switch typ {
	case "TimeRhs":
		var v contract.TimeRhs
		unmarshal(&v)
		return marshal(v)
	case "ConditionLeaf":
		var v contract.ConditionLeaf
		unmarshal(&v)
		return marshal(v)
	case "ConditionValue":
		var v contract.ConditionValue
		unmarshal(&v)
		return marshal(v)
	case "ConditionNode":
		var v contract.ConditionNode
		unmarshal(&v)
		return marshal(v)
	case "ConditionDef":
		var v contract.ConditionDef
		unmarshal(&v)
		return marshal(v)
	case "SubjectRef":
		var v contract.SubjectRef
		unmarshal(&v)
		return marshal(v)
	case "ModelIR":
		var v contract.ModelIR
		unmarshal(&v)
		return marshal(v)
	case "ReasonStep":
		var v contract.ReasonStep
		unmarshal(&v)
		return marshal(v)
	case "MissingLink":
		var v contract.MissingLink
		unmarshal(&v)
		return marshal(v)
	case "ReasonResult":
		var v contract.ReasonResult
		unmarshal(&v)
		return marshal(v)
	case "Policy":
		var v contract.Policy
		unmarshal(&v)
		return marshal(v)
	case "AuditEntry":
		var v contract.AuditEntry
		unmarshal(&v)
		return marshal(v)
	case "GrantSubject":
		var v contract.GrantSubject
		unmarshal(&v)
		return marshal(v)
	case "GrantCondition":
		var v contract.GrantCondition
		unmarshal(&v)
		return marshal(v)
	case "GrantEntry":
		var v contract.GrantEntry
		unmarshal(&v)
		return marshal(v)
	case "GrantTupleKey":
		var v contract.GrantTupleKey
		unmarshal(&v)
		return marshal(v)
	case "TupleRef":
		var v contract.TupleRef
		unmarshal(&v)
		return marshal(v)
	default:
		t.Fatalf("unknown marshal type %q", typ)
		return ""
	}
}
