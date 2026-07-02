package pdp

import (
	"context"
	"net/http"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/modules/model"
)

// 모델 핀·컨텍스트 전달을 캡처 fake로 검증한다(LFGA-26 리뷰 #21a: 구현은 있었지만 미검증이었다).
func TestEvaluate_pinsModelAndForwardsContext(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
	pins := []string{}
	ctxs := []map[string]any{}
	gw := fakeGW{allow: func(string, string) bool { return true }, pins: &pins, ctxs: &ctxs}
	body := `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"},"context":{"ip":"10.0.0.4"}}`
	w := post(t, router(deps(mr, pr, gw, &fakeRecorder{})), body)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if len(pins) != 1 || pins[0] != "m1" {
		t.Errorf("check pins = %v, want [m1]", pins)
	}
	if len(ctxs) != 1 || ctxs[0]["ip"] != "10.0.0.4" {
		t.Errorf("context passthrough = %v", ctxs)
	}
}

// reason 경로의 모든 Check도 같은 모델 버전으로 핀되는지 검증한다.
func TestEvaluate_reasonChecksArePinned(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
	pins := []string{}
	gw := fakeGW{
		allow: func(rel, obj string) bool {
			return (rel == "can_read" && obj == "document:123") || (rel == "viewer" && obj == "document:123")
		},
		tuples: func(obj, rel string) []string {
			if obj == "document:123" && rel == "viewer" {
				return []string{"user:alice"}
			}
			return nil
		},
		pins: &pins,
	}
	body := `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"},"options":{"reason":true}}`
	w := post(t, router(deps(mr, pr, gw, &fakeRecorder{})), body)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if len(pins) < 2 {
		t.Fatalf("expected multiple pinned checks (evaluate + reason), got %v", pins)
	}
	for i, p := range pins {
		if p != "m1" {
			t.Errorf("check %d pin = %q, want m1", i, p)
		}
	}
}

// 배열 context는 TS 관측 결과(OpenFGA 거부 → 감사 + 500)를 재현한다(리뷰 #3).
func TestEvaluate_arrayContext(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
	pins := []string{}
	gw := fakeGW{allow: func(string, string) bool { return true }, pins: &pins}
	rec := &fakeRecorder{}
	body := `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"},"context":[1,2]}`
	w := post(t, router(deps(mr, pr, gw, rec)), body)
	if w.Code != http.StatusInternalServerError || w.Body.String() != `{"error":"evaluation failed"}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !rec.has("pdp.evaluate.openfga_error") {
		t.Errorf("expected openfga_error audit, got %v", rec.actions)
	}
	if len(pins) != 0 {
		t.Errorf("check must not run for array context, pins=%v", pins)
	}
}
