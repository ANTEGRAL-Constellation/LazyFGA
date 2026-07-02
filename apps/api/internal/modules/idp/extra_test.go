package idp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// ── mapping 엔진 방어 분기 ──

func TestScalarValue_Branches(t *testing.T) {
	ev := &IdpEvent{Type: "e", Subject: Subject{Type: "user", ID: "a"}, Attributes: map[string]any{"weird": 5, "s": "x"}}
	if v, err := scalarValue(ev, "subject.type", nil); err != "" || v != "user" {
		t.Errorf("subject.type → %q,%q", v, err)
	}
	if _, err := scalarValue(ev, "attributes.weird", nil); err != "unresolved {{attributes.weird}}" {
		t.Errorf("non-scalar attribute → %q", err)
	}
	if _, err := scalarValue(ev, "attributes.missing", nil); err != "unresolved {{attributes.missing}}" {
		t.Errorf("missing attribute → %q", err)
	}
	if _, err := scalarValue(ev, "bogus", nil); err != "unresolved {{bogus}}" {
		t.Errorf("unknown path → %q", err)
	}
}

func TestMatchValue_ErrorPath(t *testing.T) {
	ev := &IdpEvent{Type: "e", Subject: Subject{ID: "a"}, Attributes: map[string]any{}}
	if _, ok := matchValue(ev, "attributes.missing"); ok {
		t.Error("unresolvable field must be false")
	}
	// matchRule with an unresolvable predicate field → no match.
	if matchRule(MappingRule{EventType: "e", Match: []MatchPredicate{{Field: "attributes.x", Equals: "y"}}}, ev) {
		t.Error("unresolvable predicate must not match")
	}
}

func TestRenderTuple_MoreBranches(t *testing.T) {
	ev := &IdpEvent{Type: "e", Subject: Subject{Type: "user", ID: "a"}, Attributes: map[string]any{"x": ""}}
	if _, err := renderTuple(TupleTemplate{User: "{{subject}}", Relation: "r", Object: "team:1"}, ev, nil); err != "user template must start with a literal type: prefix" {
		t.Errorf("user prefix → %q", err)
	}
	if _, err := renderTuple(TupleTemplate{User: "user:{{attributes.x}}", Relation: "r", Object: "team:1"}, ev, nil); err != `invalid user "user:" (need type:id)` {
		t.Errorf("invalid user → %q", err)
	}
	if _, err := renderTuple(TupleTemplate{User: "user:a", Relation: "r", Object: "team:{{attributes.x}}"}, ev, nil); err != `invalid object "team:" (need type:id)` {
		t.Errorf("invalid object → %q", err)
	}
}

func TestFanOutItems_NonArray(t *testing.T) {
	ev := &IdpEvent{Attributes: map[string]any{"x": "not-an-array"}}
	if got := fanOutItems(MappingRule{FanOut: strp("x")}, ev); got != nil {
		t.Errorf("non-array fanOut attribute → nil, got %+v", got)
	}
}

// ── signature 방어 분기 ──

func TestHMACKey_Base64Fallbacks(t *testing.T) {
	spec := WebhookSignatureSpec{SecretEncoding: "base64"}
	if got := hmacKey(spec, "YWI"); string(got) != "ab" { // unpadded → RawStd fallback
		t.Errorf("unpadded base64 → %q", got)
	}
	if got := hmacKey(spec, "!!!"); got != nil { // 완전 무효 → nil
		t.Errorf("invalid base64 → %v", got)
	}
}

func TestDecodeSig_Branches(t *testing.T) {
	if _, ok := decodeSig("zz", "hex"); ok {
		t.Error("invalid hex must fail")
	}
	if _, ok := decodeSig(base64.StdEncoding.EncodeToString([]byte("x")), "base64"); !ok {
		t.Error("padded base64 must decode")
	}
	if _, ok := decodeSig(base64.RawStdEncoding.EncodeToString([]byte("xyz")), "base64"); !ok {
		t.Error("unpadded base64 must decode via fallback")
	}
	if _, ok := decodeSig("!!!", "base64"); ok {
		t.Error("invalid base64 must fail")
	}
}

// ── routes helper 직접 테스트 ──

func TestValidPriority(t *testing.T) {
	if !validPriority(nil, false) {
		t.Error("absent must be valid")
	}
	if validPriority(nil, true) {
		t.Error("null present must be invalid")
	}
	if !validPriority(3.0, true) {
		t.Error("integer must be valid")
	}
	if validPriority(1.5, true) {
		t.Error("1.5 must be invalid")
	}
	if validPriority(math.Inf(1), true) {
		t.Error("Inf must be invalid")
	}
}

func TestMergeFanOut(t *testing.T) {
	if mergeFanOut(map[string]any{"fanOut": nil}, strp("old")) != nil {
		t.Error("null → nil")
	}
	if got := mergeFanOut(map[string]any{"fanOut": "new"}, strp("old")); got != "new" {
		t.Errorf("string → %v", got)
	}
	if got := mergeFanOut(map[string]any{"fanOut": 5}, strp("old")); got != "old" {
		t.Errorf("non-string present → existing, got %v", got)
	}
	if got := mergeFanOut(map[string]any{}, strp("old")); got != "old" {
		t.Errorf("absent + existing set → existing, got %v", got)
	}
	if got := mergeFanOut(map[string]any{}, nil); got != nil {
		t.Errorf("absent + existing nil → nil, got %v", got)
	}
}

func TestBuildRulePatch(t *testing.T) {
	tt := &TupleTemplate{User: "user:a", Relation: "r", Object: "team:1"}
	patch := buildRulePatch(map[string]any{
		"eventType": "e2",
		"match":     []any{map[string]any{"field": "subject", "equals": "a"}},
		"op":        "delete",
		"fanOut":    "roleKeys",
		"priority":  4.0,
	}, tt)
	if patch.EventType == nil || *patch.EventType != "e2" {
		t.Errorf("eventType = %+v", patch.EventType)
	}
	var mp []MatchPredicate
	if patch.Match == nil || json.Unmarshal(*patch.Match, &mp) != nil || len(mp) != 1 || mp[0].Field != "subject" {
		t.Errorf("match = %s", *patch.Match)
	}
	if patch.TupleTemplate != tt {
		t.Error("tupleTemplate not set")
	}
	if patch.Op == nil || *patch.Op != "delete" {
		t.Errorf("op = %+v", patch.Op)
	}
	if !patch.FanOutSet || patch.FanOutValue == nil || *patch.FanOutValue != "roleKeys" {
		t.Errorf("fanOut = %v,%v", patch.FanOutSet, patch.FanOutValue)
	}
	if patch.Priority == nil || *patch.Priority != 4 {
		t.Errorf("priority = %+v", patch.Priority)
	}

	// fanOut null → set with nil value (clear).
	cleared := buildRulePatch(map[string]any{"fanOut": nil}, nil)
	if !cleared.FanOutSet || cleared.FanOutValue != nil {
		t.Errorf("clear fanOut = %v,%v", cleared.FanOutSet, cleared.FanOutValue)
	}
}

func TestFanOutError_Direct(t *testing.T) {
	ttItem := TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "team:1"}
	conn := &PublicConnection{Provider: "zitadel"}
	if got := fanOutError(conn, "e", 5, ttItem); got != "fanOut must be a non-empty string" {
		t.Errorf("non-string fanOut → %q", got)
	}
	// preset unknown → attribute cross-check skipped → "".
	custom := &PublicConnection{Provider: "custom-not-a-preset"}
	if got := fanOutError(custom, "e", "roleKeys", ttItem); got != "" {
		t.Errorf("unknown preset → skip check, got %q", got)
	}
	// fanOut set but template does not reference {{item}}.
	if got := fanOutError(conn, "e", "roleKeys", TupleTemplate{User: "user:a", Relation: "r", Object: "team:1"}); got != "fanOut requires the tuple template to reference {{item}}" {
		t.Errorf("no item ref → %q", got)
	}
	// known attribute → "".
	if got := fanOutError(conn, "user.grant.added", "roleKeys", ttItem); got != "" {
		t.Errorf("known attribute → %q", got)
	}
}

func TestParseMatchStrict_Branches(t *testing.T) {
	if _, ok := parseMatchStrict("nope"); ok {
		t.Error("non-array must fail")
	}
	if _, ok := parseMatchStrict([]any{"x"}); ok {
		t.Error("non-object element must fail")
	}
	if _, ok := parseMatchStrict([]any{map[string]any{"field": "f"}}); ok {
		t.Error("missing equals must fail")
	}
	if got, ok := parseMatchStrict([]any{map[string]any{"field": "f", "equals": "e"}}); !ok || !reflect.DeepEqual(got, []MatchPredicate{{Field: "f", Equals: "e"}}) {
		t.Errorf("valid → %+v,%v", got, ok)
	}
}

// ── 추가 HTTP 분기 ──

func TestWebhook_ChunkedBodyTooLarge(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	req := httptest.NewRequest("POST", "/idp/webhook/zitadel", strings.NewReader(strings.Repeat("a", maxWebhookBody+10)))
	req.ContentLength = -1 // chunked: 크기 미리 알 수 없음 → 핸들러 읽기에서 MaxBytesReader가 막는다.
	w := httptest.NewRecorder()
	newRouter(d).ServeHTTP(w, req)
	assertBody(t, w, http.StatusRequestEntityTooLarge, `{"error":"payload too large"}`)
}

func TestListRules_Errors(t *testing.T) {
	t.Run("getByID error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return nil, errors.New("db") }
		w := do(newRouter(d), "GET", "/idp/connections/"+uuidC+"/rules", "", adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
	t.Run("malformed uuid → 404", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "GET", "/idp/connections/bad/rules", "", adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
	t.Run("listRules error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) {
			return &PublicConnection{ID: uuidC, Provider: "zitadel", Enabled: true}, nil
		}
		repo.listRules = func(context.Context, string) ([]StoredRule, error) { return nil, errors.New("db") }
		w := do(newRouter(d), "GET", "/idp/connections/"+uuidC+"/rules", "", adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
}

func TestCreateRule_MoreBranches(t *testing.T) {
	conn := &PublicConnection{ID: uuidC, Provider: "zitadel", Enabled: true}
	t.Run("getByID error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return nil, errors.New("db") }
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", `{}`, adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
	t.Run("fanOut non-string → 422", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return conn, nil }
		body := `{"eventType":"user.grant.added","op":"write","tupleTemplate":{"user":"user:a","object":"team:1","relation":"member"},"fanOut":5}`
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", body, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"fanOut must be a non-empty string"}`)
	})
	t.Run("createRule repo error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return conn, nil }
		repo.createRuleFn = func(context.Context, string, CreateRuleInput) (*StoredRule, error) { return nil, errors.New("db") }
		body := `{"eventType":"user.grant.added","op":"write","tupleTemplate":{"user":"user:a","object":"team:1","relation":"member"}}`
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", body, adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
}

func TestUpdateRule_MergeAndErrorBranches(t *testing.T) {
	existingFan := &StoredRule{
		ID: uuidR, ConnectionID: uuidC, EventType: "user.grant.added", Match: json.RawMessage("[]"),
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "team:{{attributes.project}}"},
		Op:            "write", Priority: 0, FanOut: strp("roleKeys"),
	}
	conn := &PublicConnection{ID: uuidC, Provider: "zitadel", Enabled: true}

	t.Run("getRule error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return nil, errors.New("db") }
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{}`, adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})

	t.Run("conn lookup error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return existingFan, nil }
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return nil, errors.New("db") }
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{}`, adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})

	t.Run("full field update (eventType/match/tt/fanOut string) → patch built", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return existingFan, nil }
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return conn, nil }
		var patch RulePatch
		repo.updateRuleFn = func(_ context.Context, _ string, p RulePatch) (*StoredRule, error) {
			patch = p
			return existingFan, nil
		}
		body := `{"eventType":"user.grant.added","match":[{"field":"subject","equals":"alice"}],"tupleTemplate":{"user":"user:{{subject}}","object":"team:{{attributes.project}}","relation":"{{item}}"},"fanOut":"roleKeys"}`
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, body, adminHdr())
		if w.Code != http.StatusOK {
			t.Fatalf("got %d body=%s", w.Code, w.Body.String())
		}
		if patch.EventType == nil || patch.Match == nil || patch.TupleTemplate == nil || !patch.FanOutSet || patch.FanOutValue == nil {
			t.Errorf("patch = %+v", patch)
		}
	})

	t.Run("absent fanOut keeps existing (merge existing) success", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return existingFan, nil }
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return conn, nil }
		repo.updateRuleFn = func(context.Context, string, RulePatch) (*StoredRule, error) { return existingFan, nil }
		// priority only; merged fanOut stays "roleKeys", template still references {{item}} → valid.
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"priority":3}`, adminHdr())
		if w.Code != http.StatusOK {
			t.Fatalf("got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("conn nil → skips merged validation", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return existingFan, nil }
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return nil, nil } // conn gone
		repo.updateRuleFn = func(context.Context, string, RulePatch) (*StoredRule, error) { return existingFan, nil }
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"priority":3}`, adminHdr())
		if w.Code != http.StatusOK {
			t.Fatalf("got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("updateRule repo error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return existingFan, nil }
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return conn, nil }
		repo.updateRuleFn = func(context.Context, string, RulePatch) (*StoredRule, error) { return nil, errors.New("db") }
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"priority":3}`, adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
}

func TestUpdateConnection_LookupError(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.getByID = func(context.Context, string) (*PublicConnection, error) { return nil, errors.New("db") }
	w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{}`, adminHdr())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.Code)
	}
	// updateConnection repo error → 500.
	repo.getByID = func(context.Context, string) (*PublicConnection, error) {
		return &PublicConnection{ID: uuidC, Provider: "z", Enabled: true}, nil
	}
	repo.updateConn = func(context.Context, string, ConnectionPatch) (*PublicConnection, error) {
		return nil, errors.New("db")
	}
	w2 := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{}`, adminHdr())
	if w2.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w2.Code)
	}
}

func TestVerify_NoneSourceWithTimestampTemplate(t *testing.T) {
	// timestampSource="none"인데 payloadTemplate이 {timestamp}를 참조하면 조립 불가 → false.
	spec := WebhookSignatureSpec{
		Header: "Sig", HeaderFormat: "scheme_hex", TimestampSource: "none", TimestampUnit: "none",
		PayloadTemplate: "{timestamp}.{body}", Algorithm: "sha256", Encoding: "hex",
		SecretEncoding: "raw", ToleranceSec: 300, AllowMultipleSignatures: false,
	}
	if VerifyWebhookSignature(spec, sigBody, hdr("Sig", "sha256="+hmacHex("s", sigBody)), "s", fixedNow) {
		t.Error("{timestamp} template with no timestamp source must fail")
	}
}

func TestUpdateConnection_InvalidBodyIsEmptyPatch(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.getByID = func(context.Context, string) (*PublicConnection, error) {
		return &PublicConnection{ID: uuidC, Provider: "z", Enabled: true}, nil
	}
	called := false
	repo.updateConn = func(_ context.Context, _ string, p ConnectionPatch) (*PublicConnection, error) {
		called = true
		if p.Preset != nil || p.SigningSecret != nil || p.Enabled != nil {
			t.Errorf("invalid body must produce empty patch, got %+v", p)
		}
		return &PublicConnection{ID: uuidC, Provider: "z", Enabled: true}, nil
	}
	w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `not-json`, adminHdr())
	if w.Code != http.StatusOK || !called {
		t.Fatalf("got %d called=%v", w.Code, called)
	}
}

func TestDelete_RepoErrors(t *testing.T) {
	t.Run("deleteConnection error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.deleteConn = func(context.Context, string) (bool, error) { return false, errors.New("db") }
		w := do(newRouter(d), "DELETE", "/idp/connections/"+uuidC, "", adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
	t.Run("deleteRule error → 500", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.deleteRuleFn = func(context.Context, string) (bool, error) { return false, errors.New("db") }
		w := do(newRouter(d), "DELETE", "/idp/rules/"+uuidR, "", adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
}
