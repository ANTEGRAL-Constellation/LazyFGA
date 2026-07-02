package idp

import (
	"encoding/json"
	"reflect"
	"testing"
)

// unJSON은 JSON 텍스트를 decode해 실제 웹훅 경로와 동일한 타입(map/[]any/float64)으로 만든다.
func unJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("unJSON(%q): %v", s, err)
	}
	return v
}

func TestExtractEvent_Zitadel(t *testing.T) {
	Z := zitadelPreset

	t.Run("signup → subject from aggregateID, org from resourceOwner", func(t *testing.T) {
		ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.human.added","aggregateID":"bob","resourceOwner":"org1","event_payload":{"userName":"bob@x"}}`))
		want := &IdpEvent{Type: "user.human.added", Subject: Subject{Type: "user", ID: "bob"}, Attributes: map[string]any{"org": "org1"}}
		if !reflect.DeepEqual(ev, want) {
			t.Fatalf("got %+v want %+v", ev, want)
		}
	})

	t.Run("self-registered also matches the signup rule", func(t *testing.T) {
		ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.human.selfregistered","aggregateID":"carol","resourceOwner":"org2"}`))
		if ev == nil || ev.Subject.ID != "carol" {
			t.Fatalf("got %+v", ev)
		}
	})

	t.Run("grant.added → subject from event_payload.userId, project from projectId", func(t *testing.T) {
		ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.grant.added","aggregateID":"grant-99","event_payload":{"userId":"alice","projectId":"eng","roleKeys":["editor","viewer"]}}`))
		want := &IdpEvent{Type: "user.grant.added", Subject: Subject{Type: "user", ID: "alice"},
			Attributes: map[string]any{"project": "eng", "roleKeys": []string{"editor", "viewer"}}}
		if !reflect.DeepEqual(ev, want) {
			t.Fatalf("got %+v want %+v", ev, want)
		}
	})

	t.Run("grant.removed → no roleKeys attribute", func(t *testing.T) {
		ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.grant.removed","aggregateID":"grant-99","event_payload":{"userId":"alice","projectId":"eng"}}`))
		want := &IdpEvent{Type: "user.grant.removed", Subject: Subject{Type: "user", ID: "alice"},
			Attributes: map[string]any{"project": "eng"}}
		if !reflect.DeepEqual(ev, want) {
			t.Fatalf("got %+v want %+v", ev, want)
		}
	})

	t.Run("unmatched event type → nil", func(t *testing.T) {
		if ev := ExtractEvent(Z, unJSON(t, `{"event_type":"session.created","aggregateID":"x"}`)); ev != nil {
			t.Fatalf("expected nil, got %+v", ev)
		}
	})

	t.Run("matched but missing/empty/non-string subject → nil", func(t *testing.T) {
		if ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.grant.added","event_payload":{"projectId":"eng"}}`)); ev != nil {
			t.Errorf("no userId: expected nil, got %+v", ev)
		}
		if ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.grant.added","event_payload":{"userId":"","projectId":"eng"}}`)); ev != nil {
			t.Errorf("empty userId: expected nil, got %+v", ev)
		}
		if ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.grant.added","event_payload":{"userId":123,"projectId":"eng"}}`)); ev != nil {
			t.Errorf("numeric userId: expected nil, got %+v", ev)
		}
	})

	t.Run("numeric scalar attribute is coerced to string", func(t *testing.T) {
		ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":42}}`))
		if ev == nil || ev.Attributes["project"] != "42" {
			t.Fatalf("got %+v", ev)
		}
	})

	t.Run("malformed / non-object body → nil (no crash)", func(t *testing.T) {
		if ExtractEvent(Z, nil) != nil {
			t.Error("nil body")
		}
		if ExtractEvent(Z, float64(42)) != nil {
			t.Error("number body")
		}
		if ExtractEvent(Z, "nope") != nil {
			t.Error("string body")
		}
		if ExtractEvent(Z, unJSON(t, `{"nope":1}`)) != nil {
			t.Error("no event_type")
		}
	})

	t.Run("array of non-scalars is filtered to scalar elements only", func(t *testing.T) {
		ev := ExtractEvent(Z, unJSON(t, `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng","roleKeys":["editor",{"x":1},7]}}`))
		want := []string{"editor", "7"}
		if ev == nil || !reflect.DeepEqual(ev.Attributes["roleKeys"], want) {
			t.Fatalf("got %+v", ev)
		}
	})
}

func TestExtractEvent_StandardWebhooks(t *testing.T) {
	SW := standardWebhooksPreset
	ev := ExtractEvent(SW, unJSON(t, `{"type":"user.created","data":{"id":"u1","orgId":"acme"}}`))
	want := &IdpEvent{Type: "user.created", Subject: Subject{Type: "user", ID: "u1"}, Attributes: map[string]any{"org": "acme"}}
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("got %+v want %+v", ev, want)
	}
}

func TestExtractEvent_DangerousKeyGuard(t *testing.T) {
	evil := ProviderPreset{
		Signature: zitadelPreset.Signature,
		TypePath:  "type",
		Extraction: []EventExtractionRule{{
			Match: []string{"x"}, SubjectType: "user", SubjectIDPath: "__proto__.polluted",
			AttributePaths: []AttributePath{{Name: "c", Path: "constructor.name"}},
		}},
	}
	if ExtractEvent(evil, unJSON(t, `{"type":"x","a":1}`)) != nil {
		t.Error("__proto__/constructor segments must resolve to nothing → nil subject")
	}
}

func TestReadEventType(t *testing.T) {
	Z := zitadelPreset
	if s, ok := readEventType(Z, unJSON(t, `{"event_type":"user.grant.added"}`)); !ok || s != "user.grant.added" {
		t.Errorf("got %q,%v", s, ok)
	}
	if _, ok := readEventType(Z, unJSON(t, `{"nope":1}`)); ok {
		t.Error("no event_type must be false")
	}
	if _, ok := readEventType(Z, nil); ok {
		t.Error("nil body must be false")
	}
	if _, ok := readEventType(Z, unJSON(t, `{"event_type":""}`)); ok {
		t.Error("empty event_type must be false")
	}
}

func TestAttributeNamesForEvent(t *testing.T) {
	Z := zitadelPreset
	if got := attributeNamesForEvent(Z, "user.grant.added"); !reflect.DeepEqual(got, []string{"project", "roleKeys"}) {
		t.Errorf("grant.added: %v", got)
	}
	if got := attributeNamesForEvent(Z, "user.grant.removed"); !reflect.DeepEqual(got, []string{"project"}) {
		t.Errorf("grant.removed: %v", got)
	}
	if got := attributeNamesForEvent(Z, "user.human.added"); !reflect.DeepEqual(got, []string{"org"}) {
		t.Errorf("human.added: %v", got)
	}
	if got := attributeNamesForEvent(Z, "unknown"); len(got) != 0 {
		t.Errorf("unknown: %v", got)
	}
}

func TestGetPath_ArrayIndexTraversal(t *testing.T) {
	body := unJSON(t, `{"list":["a","b",{"k":"v"}]}`)
	if got := getPath(body, "list.1"); got != "b" {
		t.Errorf("list.1 = %v, want b", got)
	}
	if got := getPath(body, "list.2.k"); got != "v" {
		t.Errorf("list.2.k = %v, want v", got)
	}
	if got := getPath(body, "list.3"); got != nil {
		t.Errorf("out-of-range index must be nil, got %v", got)
	}
	if got := getPath(body, "list.00"); got != nil {
		t.Errorf("non-canonical index 00 must be nil, got %v", got)
	}
	if got := getPath(body, "list.-1"); got != nil {
		t.Errorf("negative index must be nil, got %v", got)
	}
	if got := getPath(body, "list.length"); got != float64(3) {
		t.Errorf("array length is a JS own property (hasOwnProperty true) → 3, got %v", got)
	}
	if got := getPath("scalar", "x"); got != nil {
		t.Errorf("path into scalar must be nil, got %v", got)
	}
}

func TestArrayIndex(t *testing.T) {
	cases := []struct {
		key    string
		length int
		idx    int
		ok     bool
	}{
		{"0", 1, 0, true},
		{"2", 3, 2, true},
		{"", 3, 0, false},
		{"00", 3, 0, false},
		{"01", 3, 0, false},
		{"-1", 3, 0, false},
		{"3", 3, 0, false},
		{"x", 3, 0, false},
		{"0", 0, 0, false},
	}
	for _, c := range cases {
		idx, ok := arrayIndex(c.key, c.length)
		if idx != c.idx || ok != c.ok {
			t.Errorf("arrayIndex(%q,%d) = (%d,%v), want (%d,%v)", c.key, c.length, idx, ok, c.idx, c.ok)
		}
	}
}

func TestCoerceScalar(t *testing.T) {
	if v, ok := coerceScalar(true); !ok || v != "true" {
		t.Errorf("bool true → %q,%v", v, ok)
	}
	if v, ok := coerceScalar(false); !ok || v != "false" {
		t.Errorf("bool false → %q,%v", v, ok)
	}
	if v, ok := coerceScalar(42.0); !ok || v != "42" {
		t.Errorf("number → %q,%v", v, ok)
	}
	if v, ok := coerceScalar("s"); !ok || v != "s" {
		t.Errorf("string → %q,%v", v, ok)
	}
	if _, ok := coerceScalar([]any{1}); ok {
		t.Error("array must not coerce")
	}
	if _, ok := coerceScalar(nil); ok {
		t.Error("nil must not coerce")
	}
}
