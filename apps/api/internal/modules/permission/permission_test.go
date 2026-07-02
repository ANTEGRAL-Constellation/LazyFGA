package permission

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/modules/model"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga/writeerror"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
	"github.com/go-chi/chi/v5"
)

// ---- fakes ----

type stubAuth struct{ p httpx.Principal }

func (s stubAuth) Authenticate(context.Context, string) (httpx.Principal, error) { return s.p, nil }

type fakeGateway struct {
	writeErr     error
	readFn       func(openfga.ReadInput) ([]openfga.ReadTuple, error)
	writes       int
	lastWrite    openfga.WriteInput
	lastWritePin string
}

func (f *fakeGateway) Write(_ context.Context, in openfga.WriteInput, opts ...openfga.WriteOption) error {
	f.writes++
	f.lastWrite = in
	f.lastWritePin = openfga.ResolveWriteAuthorizationModelID(opts...)
	return f.writeErr
}
func (f *fakeGateway) Read(_ context.Context, in openfga.ReadInput) ([]openfga.ReadTuple, error) {
	if f.readFn != nil {
		return f.readFn(in)
	}
	return nil, nil
}

type fakeModel struct {
	fn func(context.Context) (*model.Version, error)
}

func (f fakeModel) CurrentVersion(ctx context.Context) (*model.Version, error) { return f.fn(ctx) }

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

func modelWithFixture(t *testing.T) fakeModel {
	t.Helper()
	data, err := os.ReadFile(testutil.RepoPath("packages", "shared", "src", "__fixtures__", "doc-folder-team.ir.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return fakeModel{fn: func(context.Context) (*model.Version, error) {
		return &model.Version{ID: "v1", AuthorizationModelID: "m1", IRJSON: data}, nil
	}}
}

func router(deps Deps) *chi.Mux {
	r := chi.NewRouter()
	r.NotFound(httpx.WriteHonoNotFound)
	Mount(r, deps)
	return r
}

func adminDeps(mr ModelReader, gw Gateway, rec Recorder) Deps {
	return Deps{Model: mr, Gateway: gw, Recorder: rec, Auth: stubAuth{p: httpx.Principal{Role: httpx.RoleAdmin}}}
}

func req(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rq := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, rq)
	return w
}

const validGrant = `{"subject":{"type":"user","id":"bob"},"relation":"viewer","resource":{"type":"document","id":"1"}}`

// ---- POST /grants ----

func TestGrant(t *testing.T) {
	t.Run("malformed -> 400", func(t *testing.T) {
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, &fakeRecorder{})), http.MethodPost, "/grants", `{"subject":{}}`)
		if w.Code != 400 || w.Body.String() != `{"error":"malformed grant request","code":"malformed_request"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("no model -> 404", func(t *testing.T) {
		mr := fakeModel{fn: func(context.Context) (*model.Version, error) { return nil, nil }}
		w := req(t, router(adminDeps(mr, &fakeGateway{}, &fakeRecorder{})), http.MethodPost, "/grants", validGrant)
		if w.Code != 404 || w.Body.String() != `{"error":"no model has been published yet","code":"no_published_model"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("validation fail -> 400", func(t *testing.T) {
		body := `{"subject":{"type":"user","id":"bob"},"relation":"can_read","resource":{"type":"document","id":"1"}}`
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, &fakeRecorder{})), http.MethodPost, "/grants", body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"relation_not_assignable"`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("created -> 201", func(t *testing.T) {
		rec := &fakeRecorder{}
		gw := &fakeGateway{}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, rec)), http.MethodPost, "/grants", validGrant)
		if w.Code != 201 || w.Body.String() != `{"granted":true,"created":true}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		if gw.writes != 1 || !rec.has("permission.grant") {
			t.Errorf("writes=%d audit=%v", gw.writes, rec.actions)
		}
		// 발행본 모델 핀과 tuple 내용이 gateway까지 전달되는지 캡처로 검증(리뷰 #21a).
		if gw.lastWritePin != "m1" {
			t.Errorf("write pin = %q, want m1", gw.lastWritePin)
		}
		if len(gw.lastWrite.Writes) != 1 || gw.lastWrite.Writes[0].User != "user:bob" ||
			gw.lastWrite.Writes[0].Relation != "viewer" || gw.lastWrite.Writes[0].Object != "document:1" {
			t.Errorf("unexpected write tuple: %+v", gw.lastWrite)
		}
	})
	t.Run("idempotent noop -> 200 not audited", func(t *testing.T) {
		rec := &fakeRecorder{}
		gw := &fakeGateway{writeErr: errors.New("write_failed_due_to_invalid_input: cannot write a tuple which already exists")}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, rec)), http.MethodPost, "/grants", validGrant)
		if w.Code != 200 || w.Body.String() != `{"granted":true,"created":false}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		if rec.has("permission.grant") {
			t.Error("noop should not be audited")
		}
	})
	t.Run("transient -> 502", func(t *testing.T) {
		gw := &fakeGateway{writeErr: errors.New("fetch failed")}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, &fakeRecorder{})), http.MethodPost, "/grants", validGrant)
		if w.Code != 502 || !strings.Contains(w.Body.String(), `"code":"openfga_unavailable"`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("deterministic -> 400", func(t *testing.T) {
		gw := &fakeGateway{writeErr: errors.New("relation not found")}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, &fakeRecorder{})), http.MethodPost, "/grants", validGrant)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"openfga_invalid_input"`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("model error -> 500", func(t *testing.T) {
		mr := fakeModel{fn: func(context.Context) (*model.Version, error) { return nil, errors.New("db down") }}
		w := req(t, router(adminDeps(mr, &fakeGateway{}, &fakeRecorder{})), http.MethodPost, "/grants", validGrant)
		if w.Code != 500 || w.Body.String() != "Internal Server Error" {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("bad IR -> 500", func(t *testing.T) {
		mr := fakeModel{fn: func(context.Context) (*model.Version, error) {
			return &model.Version{IRJSON: []byte("not json")}, nil
		}}
		w := req(t, router(adminDeps(mr, &fakeGateway{}, &fakeRecorder{})), http.MethodPost, "/grants", validGrant)
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// ---- DELETE /grants ----

func TestRevoke(t *testing.T) {
	t.Run("malformed -> 400", func(t *testing.T) {
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, &fakeRecorder{})), http.MethodDelete, "/grants", `{}`)
		if w.Code != 400 || w.Body.String() != `{"error":"malformed revoke request","code":"malformed_request"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("deleted -> 200 audited", func(t *testing.T) {
		rec := &fakeRecorder{}
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, rec)), http.MethodDelete, "/grants", validGrant)
		if w.Code != 200 || w.Body.String() != `{"revoked":true,"deleted":true}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		if !rec.has("permission.revoke") {
			t.Error("expected permission.revoke audit")
		}
	})
	t.Run("noop delete -> 200 not audited", func(t *testing.T) {
		rec := &fakeRecorder{}
		gw := &fakeGateway{writeErr: errors.New("write_failed_due_to_invalid_input: cannot delete a tuple which does not exist")}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, rec)), http.MethodDelete, "/grants", validGrant)
		if w.Code != 200 || w.Body.String() != `{"revoked":true,"deleted":false}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		if rec.has("permission.revoke") {
			t.Error("noop should not be audited")
		}
	})
	t.Run("validation fail -> 400", func(t *testing.T) {
		body := `{"subject":{"type":"user","id":"bob"},"relation":"can_read","resource":{"type":"document","id":"1"}}`
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, &fakeRecorder{})), http.MethodDelete, "/grants", body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"relation_not_assignable"`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("transient -> 502", func(t *testing.T) {
		gw := &fakeGateway{writeErr: errors.New("fetch failed")}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, &fakeRecorder{})), http.MethodDelete, "/grants", validGrant)
		if w.Code != 502 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("no model -> 404", func(t *testing.T) {
		mr := fakeModel{fn: func(context.Context) (*model.Version, error) { return nil, nil }}
		w := req(t, router(adminDeps(mr, &fakeGateway{}, &fakeRecorder{})), http.MethodDelete, "/grants", validGrant)
		if w.Code != 404 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// ---- GET /grants ----

func TestList(t *testing.T) {
	t.Run("both -> 400", func(t *testing.T) {
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, nil)), http.MethodGet, "/grants?resource=document:1&subject=user:bob", "")
		if w.Code != 400 || w.Body.String() != "{\"error\":\"supply exactly one of `resource` or `subject`\",\"code\":\"malformed_request\"}" {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("neither -> 400", func(t *testing.T) {
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, nil)), http.MethodGet, "/grants", "")
		if w.Code != 400 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("invalid resource -> 400", func(t *testing.T) {
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, nil)), http.MethodGet, "/grants?resource=bad", "")
		if w.Code != 400 || w.Body.String() != `{"error":"invalid resource \"bad\"","code":"malformed_request"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("by resource ok + assignable filter", func(t *testing.T) {
		gw := &fakeGateway{readFn: func(openfga.ReadInput) ([]openfga.ReadTuple, error) {
			return []openfga.ReadTuple{
				{User: "user:bob", Relation: "viewer", Object: "document:1"},
				{User: "folder:9", Relation: "parent", Object: "document:1"}, // parent는 배정불가 → 필터.
			}, nil
		}}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, nil)), http.MethodGet, "/grants?resource=document:1", "")
		want := `{"grants":[{"subject":{"type":"user","id":"bob"},"relation":"viewer","resource":{"type":"document","id":"1"}}]}`
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
	})
	t.Run("by resource read transient -> 502", func(t *testing.T) {
		gw := &fakeGateway{readFn: func(openfga.ReadInput) ([]openfga.ReadTuple, error) { return nil, errors.New("fetch failed") }}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, nil)), http.MethodGet, "/grants?resource=document:1", "")
		if w.Code != 502 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("by resource read deterministic -> 400", func(t *testing.T) {
		gw := &fakeGateway{readFn: func(openfga.ReadInput) ([]openfga.ReadTuple, error) { return nil, errors.New("type not found") }}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, nil)), http.MethodGet, "/grants?resource=document:1", "")
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"openfga_invalid_input"`) {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("invalid subject -> 400", func(t *testing.T) {
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, nil)), http.MethodGet, "/grants?subject=bad", "")
		if w.Code != 400 || w.Body.String() != `{"error":"invalid subject \"bad\"","code":"malformed_request"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("invalid resourceType -> 400", func(t *testing.T) {
		w := req(t, router(adminDeps(modelWithFixture(t), &fakeGateway{}, nil)), http.MethodGet, "/grants?subject=user:bob&resourceType=bad-type", "")
		if w.Code != 400 || w.Body.String() != `{"error":"invalid resourceType \"bad-type\"","code":"malformed_request"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("by subject fan-out", func(t *testing.T) {
		gw := &fakeGateway{readFn: func(in openfga.ReadInput) ([]openfga.ReadTuple, error) {
			if in.Object != nil && *in.Object == "document:" {
				return []openfga.ReadTuple{{User: "user:bob", Relation: "viewer", Object: "document:1"}}, nil
			}
			return nil, nil
		}}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, nil)), http.MethodGet, "/grants?subject=user:bob", "")
		want := `{"grants":[{"subject":{"type":"user","id":"bob"},"relation":"viewer","resource":{"type":"document","id":"1"}}]}`
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
	})
	t.Run("by subject with resourceType", func(t *testing.T) {
		seen := ""
		gw := &fakeGateway{readFn: func(in openfga.ReadInput) ([]openfga.ReadTuple, error) {
			if in.Object != nil {
				seen = *in.Object
			}
			return nil, nil
		}}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, nil)), http.MethodGet, "/grants?subject=user:bob&resourceType=folder", "")
		if w.Code != 200 || w.Body.String() != `{"grants":[]}` || seen != "folder:" {
			t.Fatalf("code=%d body=%s seen=%q", w.Code, w.Body.String(), seen)
		}
	})
	t.Run("by subject read error -> 502", func(t *testing.T) {
		gw := &fakeGateway{readFn: func(openfga.ReadInput) ([]openfga.ReadTuple, error) { return nil, errors.New("fetch failed") }}
		w := req(t, router(adminDeps(modelWithFixture(t), gw, nil)), http.MethodGet, "/grants?subject=user:bob&resourceType=folder", "")
		if w.Code != 502 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("no model -> 404", func(t *testing.T) {
		mr := fakeModel{fn: func(context.Context) (*model.Version, error) { return nil, nil }}
		w := req(t, router(adminDeps(mr, &fakeGateway{}, nil)), http.MethodGet, "/grants?resource=document:1", "")
		if w.Code != 404 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// ---- unit: interpretWriteError, decode, tupleToEntry, New ----

func TestInterpretWriteError(t *testing.T) {
	noop, gerr := interpretWriteError(errors.New("write_failed_due_to_invalid_input: cannot write a tuple which already exists"), writeerror.OpWrite)
	if !noop || gerr != nil {
		t.Fatalf("idempotent write: noop=%v gerr=%v", noop, gerr)
	}
	noop, gerr = interpretWriteError(errors.New("write_failed_due_to_invalid_input: cannot delete a tuple which does not exist"), writeerror.OpDelete)
	if !noop || gerr != nil {
		t.Fatalf("idempotent delete: noop=%v gerr=%v", noop, gerr)
	}
	_, gerr = interpretWriteError(errors.New("fetch failed"), writeerror.OpWrite)
	if gerr == nil || gerr.Status != 502 || gerr.Code != "openfga_unavailable" {
		t.Fatalf("transient: %+v", gerr)
	}
	_, gerr = interpretWriteError(errors.New("relation not found"), writeerror.OpWrite)
	if gerr == nil || gerr.Status != 400 || gerr.Code != "openfga_invalid_input" {
		t.Fatalf("deterministic: %+v", gerr)
	}
}

func TestDecodeGrantRequest(t *testing.T) {
	ok := []string{
		`{"subject":{"type":"user","id":"a"},"relation":"viewer","resource":{"type":"document","id":"1"}}`,
		`{"subject":{"type":"team","id":"e","relation":"member"},"relation":"viewer","resource":{"type":"document","id":"1"},"condition":{"name":"c"}}`,
		`{"subject":{"type":"user","id":"a"},"relation":"viewer","resource":{"type":"document","id":"1"},"condition":{"name":"c","context":{"k":1}}}`,
	}
	for _, s := range ok {
		if _, valid := decodeGrantRequest([]byte(s)); !valid {
			t.Errorf("expected valid: %s", s)
		}
	}
	bad := []string{
		`not json`, `[]`, `null`,
		`{"relation":"viewer","resource":{"type":"document","id":"1"}}`,                                                                  // subject 부재.
		`{"subject":{"id":"a"},"relation":"viewer","resource":{"type":"document","id":"1"}}`,                                             // subject.type 부재.
		`{"subject":{"type":"user"},"relation":"viewer","resource":{"type":"document","id":"1"}}`,                                        // subject.id 부재.
		`{"subject":{"type":"user","id":"a","relation":5},"relation":"viewer","resource":{"type":"d","id":"1"}}`,                         // subject.relation 비-string.
		`{"subject":{"type":"user","id":"a"},"resource":{"type":"document","id":"1"}}`,                                                   // relation 부재.
		`{"subject":{"type":"user","id":"a"},"relation":"viewer"}`,                                                                       // resource 부재.
		`{"subject":{"type":"user","id":"a"},"relation":"viewer","resource":{"id":"1"}}`,                                                 // resource.type 부재.
		`{"subject":{"type":"user","id":"a"},"relation":"viewer","resource":{"type":"d"}}`,                                               // resource.id 부재.
		`{"subject":{"type":"user","id":"a"},"relation":"viewer","resource":{"type":"d","id":"1"},"condition":5}`,                        // condition 비-object.
		`{"subject":{"type":"user","id":"a"},"relation":"viewer","resource":{"type":"d","id":"1"},"condition":{}}`,                       // condition.name 부재.
		`{"subject":{"type":"user","id":"a"},"relation":"viewer","resource":{"type":"d","id":"1"},"condition":{"name":"c","context":5}}`, // context 비-object.
	}
	for _, s := range bad {
		if _, valid := decodeGrantRequest([]byte(s)); valid {
			t.Errorf("expected invalid: %s", s)
		}
	}
}

func TestDecodeRevokeRequest(t *testing.T) {
	if _, ok := decodeRevokeRequest([]byte(validGrant)); !ok {
		t.Error("valid revoke should decode")
	}
	if _, ok := decodeRevokeRequest([]byte(`{"subject":{}}`)); ok {
		t.Error("malformed revoke should fail")
	}
}

func TestTupleToEntry(t *testing.T) {
	// userset + condition.
	e := tupleToEntry(openfga.ReadTuple{
		User: "team:eng#member", Relation: "viewer", Object: "document:1",
		Condition: &openfga.TupleCondition{Name: "biz_hours", Context: map[string]any{"tz": "UTC"}},
	})
	if e.Subject.Type != "team" || e.Subject.ID != "eng" || e.Subject.Relation == nil || *e.Subject.Relation != "member" {
		t.Fatalf("subject=%+v", e.Subject)
	}
	if e.Condition == nil || e.Condition.Name != "biz_hours" {
		t.Fatalf("condition=%+v", e.Condition)
	}
	// concrete user, no condition.
	e2 := tupleToEntry(openfga.ReadTuple{User: "user:bob", Relation: "owner", Object: "folder:1"})
	if e2.Subject.Relation != nil || e2.Condition != nil {
		t.Fatalf("entry=%+v", e2)
	}
}

func TestNew(t *testing.T) {
	d := New(fakeModel{}, &fakeGateway{}, &fakeRecorder{}, stubAuth{})
	if d.Model == nil || d.Gateway == nil || d.Recorder == nil {
		t.Fatalf("incomplete deps: %+v", d)
	}
}
