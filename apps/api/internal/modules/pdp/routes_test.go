package pdp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/modules/model"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
	"github.com/go-chi/chi/v5"
)

type stubAuth struct{ p httpx.Principal }

func (s stubAuth) Authenticate(context.Context, string) (httpx.Principal, error) { return s.p, nil }

type fakeModelReader struct {
	fn func(context.Context) (*model.Version, error)
}

func (f fakeModelReader) CurrentVersion(ctx context.Context) (*model.Version, error) {
	return f.fn(ctx)
}

type fakePolicyReader struct {
	fn func(context.Context, string, string) (*contract.Policy, error)
}

func (f fakePolicyReader) FindByActionResource(ctx context.Context, p, rt string) (*contract.Policy, error) {
	return f.fn(ctx, p, rt)
}

type fakeRecorder struct{ actions []string }

func (f *fakeRecorder) Record(action string, _ map[string]any, _ string) {
	f.actions = append(f.actions, action)
}
func (f *fakeRecorder) has(a string) bool {
	for _, x := range f.actions {
		if x == a {
			return true
		}
	}
	return false
}

func fixtureVersion(t *testing.T) *model.Version {
	t.Helper()
	data, err := os.ReadFile(testutil.RepoPath("packages", "shared", "src", "__fixtures__", "doc-folder-team.ir.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return &model.Version{ID: "v1", AuthorizationModelID: "m1", IRJSON: data}
}

func router(deps Deps) *chi.Mux {
	r := chi.NewRouter()
	r.NotFound(httpx.WriteHonoNotFound)
	Mount(r, deps)
	return r
}

func post(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/access/v1/evaluation", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func deps(mr ModelReader, pr PolicyReader, gw Gateway, rec Recorder) Deps {
	return Deps{Model: mr, Policy: pr, Gateway: gw, Recorder: rec, Auth: stubAuth{p: httpx.Principal{Role: httpx.RoleService}}}
}

const validBody = `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"}}`

func policyFound(context.Context, string, string) (*contract.Policy, error) {
	return &contract.Policy{ID: "p", Permission: "read", ResourceType: "document"}, nil
}

func TestEvaluate_badRequest(t *testing.T) {
	d := deps(nil, nil, nil, &fakeRecorder{})
	for _, body := range []string{`not json`, `{}`, `{"subject":{"type":"user"},"action":{"name":"read"},"resource":{"type":"d","id":"1"}}`, `[]`, `{"subject":{"type":"user","id":""},"action":{"name":"read"},"resource":{"type":"d","id":"1"}}`} {
		w := post(t, router(d), body)
		if w.Code != 400 || w.Body.String() != `{"error":"subject{type,id}, action{name}, resource{type,id} are required (non-empty)"}` {
			t.Fatalf("body=%q -> code=%d resp=%s", body, w.Code, w.Body.String())
		}
	}
}

func TestEvaluate_modelNotPublished(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return nil, nil }}
	w := post(t, router(deps(mr, nil, nil, &fakeRecorder{})), validBody)
	if w.Code != 200 || w.Body.String() != `{"decision":false,"context":{"reason_code":"MODEL_NOT_PUBLISHED"}}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEvaluate_noPolicy(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: func(context.Context, string, string) (*contract.Policy, error) { return nil, nil }}
	w := post(t, router(deps(mr, pr, nil, &fakeRecorder{})), validBody)
	if w.Code != 200 || w.Body.String() != `{"decision":false,"context":{"reason_code":"NO_POLICY"}}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEvaluate_decisionOnly(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
	gw := fakeGW{allow: func(string, string) bool { return true }}
	w := post(t, router(deps(mr, pr, gw, &fakeRecorder{})), validBody)
	if w.Code != 200 || w.Body.String() != `{"decision":true}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	// deny도 확인.
	gwDeny := fakeGW{allow: func(string, string) bool { return false }}
	w2 := post(t, router(deps(mr, pr, gwDeny, &fakeRecorder{})), validBody)
	if w2.Body.String() != `{"decision":false}` {
		t.Fatalf("body=%s", w2.Body.String())
	}
}

func TestEvaluate_withReason(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
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
	}
	body := `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"},"options":{"reason":true}}`
	w := post(t, router(deps(mr, pr, gw, &fakeRecorder{})), body)
	want := `{"decision":true,"context":{"reason":{"decision":true,"path":[{"via":"role","role":"viewer","on":"document","direct":true}],"text":"user:alice can read document:123: role viewer (direct)"}}}`
	if w.Code != 200 || w.Body.String() != want {
		t.Fatalf("code=%d\nbody=%s\nwant=%s", w.Code, w.Body.String(), want)
	}
}

func TestEvaluate_checkError(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
	gw := fakeGW{checkErr: errors.New("openfga down")}
	rec := &fakeRecorder{}
	w := post(t, router(deps(mr, pr, gw, rec)), validBody)
	if w.Code != 500 || w.Body.String() != `{"error":"evaluation failed"}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !rec.has("pdp.evaluate.openfga_error") {
		t.Error("expected openfga_error audit")
	}
}

func TestEvaluate_reasonSwallowedOnReadError(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
	// 모든 check는 성공(top check + role check), reason의 Read만 에러 → reason 실패 swallow.
	gw := fakeGW{allow: func(string, string) bool { return true }, readErr: errors.New("read boom")}
	rec := &fakeRecorder{}
	body := `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"},"options":{"reason":true}}`
	w := post(t, router(deps(mr, pr, gw, rec)), body)
	if w.Code != 200 || w.Body.String() != `{"decision":true}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !rec.has("pdp.reason.error") {
		t.Error("expected pdp.reason.error audit")
	}
}

func TestEvaluate_reasonSwallowedOnBadIR(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) {
		return &model.Version{ID: "v1", AuthorizationModelID: "m1", IRJSON: []byte("not json")}, nil
	}}
	pr := fakePolicyReader{fn: policyFound}
	gw := fakeGW{allow: func(string, string) bool { return true }}
	rec := &fakeRecorder{}
	body := `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"},"options":{"reason":true}}`
	w := post(t, router(deps(mr, pr, gw, rec)), body)
	if w.Code != 200 || w.Body.String() != `{"decision":true}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !rec.has("pdp.reason.error") {
		t.Error("expected pdp.reason.error audit on bad IR")
	}
}

func TestEvaluate_modelError(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return nil, errors.New("db down") }}
	w := post(t, router(deps(mr, nil, nil, &fakeRecorder{})), validBody)
	if w.Code != 500 || w.Body.String() != "Internal Server Error" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEvaluate_policyError(t *testing.T) {
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: func(context.Context, string, string) (*contract.Policy, error) { return nil, errors.New("db down") }}
	w := post(t, router(deps(mr, pr, nil, &fakeRecorder{})), validBody)
	if w.Code != 500 || w.Body.String() != "Internal Server Error" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEvaluate_contextAndOptionsFalsy(t *testing.T) {
	// context가 object면 통과, options.reason falsy면 reason 미부착.
	mr := fakeModelReader{fn: func(context.Context) (*model.Version, error) { return fixtureVersion(t), nil }}
	pr := fakePolicyReader{fn: policyFound}
	body := `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"123"},"context":{"ip":"1.2.3.4"},"options":{"reason":false}}`
	w := post(t, router(deps(mr, pr, fakeGW{allow: func(string, string) bool { return true }}, &fakeRecorder{})), body)
	if w.Code != 200 || w.Body.String() != `{"decision":true}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestJsTruthy(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{nil, false}, {true, true}, {false, false}, {float64(0), false}, {float64(1), true},
		{"", false}, {"x", true}, {map[string]any{}, true},
	}
	for _, c := range cases {
		if got := jsTruthy(c.v); got != c.want {
			t.Errorf("jsTruthy(%v) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestNew(t *testing.T) {
	d := New(fakeModelReader{}, fakePolicyReader{}, fakeGW{}, &fakeRecorder{}, stubAuth{})
	if d.Model == nil || d.Policy == nil || d.Gateway == nil || d.Recorder == nil {
		t.Fatalf("incomplete deps: %+v", d)
	}
}
