package idp

import (
	"encoding/base64"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

// applyOne은 한 이벤트를 규칙으로 적용하고 생성된 tuple을 수집한다(write는 전부 "applied").
func applyOne(t *testing.T, ev *IdpEvent, rules []MappingRule) []RenderedTuple {
	t.Helper()
	if ev == nil {
		t.Fatal("event must not be nil")
	}
	var tuples []RenderedTuple
	deps := ApplyDeps{
		WriteTuple: func(_ string, tup RenderedTuple) (string, error) { tuples = append(tuples, tup); return "applied", nil },
		Audit:      func(string, map[string]any) {},
	}
	if _, err := ApplyEvents([]IdpEvent{*ev}, rules, deps); err != nil {
		t.Fatalf("ApplyEvents: %v", err)
	}
	return tuples
}

func TestEndToEnd_Zitadel(t *testing.T) {
	preset := zitadelPreset
	const secret = "dev-zitadel-signing-secret"
	teamRule := MappingRule{
		EventType:     "user.grant.added",
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"},
		Op:            "write",
	}

	t.Run("grant.added → user:alice member team:eng", func(t *testing.T) {
		body := encJSON(map[string]any{"event_type": "user.grant.added", "event_payload": map[string]any{"userId": "alice", "projectId": "eng"}})
		ts := strconv.FormatInt(testNowSec, 10)
		sig := "t=" + ts + ",v1=" + hmacHex(secret, []byte(ts+"."), body)
		if !VerifyWebhookSignature(preset.Signature, body, hdr("ZITADEL-Signature", sig), secret, fixedNow) {
			t.Fatal("signature must verify")
		}
		ev := ExtractEvent(preset, unJSON(t, string(body)))
		got := applyOne(t, ev, []MappingRule{teamRule})
		want := []RenderedTuple{{User: "user:alice", Relation: "member", Object: "team:eng"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("optional roleKeys fan-out → one role tuple per key", func(t *testing.T) {
		ev := ExtractEvent(preset, unJSON(t, `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng","roleKeys":["editor","viewer"]}}`))
		fanRule := MappingRule{
			EventType:     "user.grant.added",
			TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "project:{{attributes.project}}"},
			Op:            "write",
			FanOut:        strp("roleKeys"),
		}
		tuples := applyOne(t, ev, []MappingRule{fanRule})
		rels := []string{tuples[0].Relation, tuples[1].Relation}
		sort.Strings(rels)
		if !reflect.DeepEqual(rels, []string{"editor", "viewer"}) {
			t.Fatalf("rels=%v", rels)
		}
		for _, tup := range tuples {
			if tup.Object != "project:eng" {
				t.Fatalf("object=%q", tup.Object)
			}
		}
	})
}

func TestEndToEnd_StandardWebhooks(t *testing.T) {
	preset := standardWebhooksPreset
	const secret = "whsec_c2VjcmV0a2V5"
	key, _ := base64.StdEncoding.DecodeString("c2VjcmV0a2V5")

	body := encJSON(map[string]any{"type": "user.created", "data": map[string]any{"id": "u1", "orgId": "acme"}})
	id := "msg_1"
	ts := strconv.FormatInt(testNowSec, 10)
	sig := hmacB64(key, []byte(id+"."+ts+"."), body)
	headers := hdr("webhook-id", id, "webhook-timestamp", ts, "webhook-signature", "v1,"+sig)
	if !VerifyWebhookSignature(preset.Signature, body, headers, secret, fixedNow) {
		t.Fatal("signature must verify")
	}
	ev := ExtractEvent(preset, unJSON(t, string(body)))
	rule := MappingRule{
		EventType:     "user.created",
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "org:{{attributes.org}}"},
		Op:            "write",
	}
	got := applyOne(t, ev, []MappingRule{rule})
	want := []RenderedTuple{{User: "user:u1", Relation: "member", Object: "org:acme"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestPresetByKey(t *testing.T) {
	if _, ok := presetByKey("zitadel"); !ok {
		t.Error("zitadel must be known")
	}
	if _, ok := presetByKey("standard-webhooks"); !ok {
		t.Error("standard-webhooks must be known")
	}
	if _, ok := presetByKey("nope"); ok {
		t.Error("unknown key must be false")
	}
	if !presetKnown("zitadel") || presetKnown("nope") {
		t.Error("presetKnown mismatch")
	}
}
