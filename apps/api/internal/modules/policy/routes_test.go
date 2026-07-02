package policy

import (
	"context"
	"encoding/json"
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
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- fakes ----

type stubAuth struct{ p httpx.Principal }

func (s stubAuth) Authenticate(context.Context, string) (httpx.Principal, error) { return s.p, nil }

type fakeStore struct {
	findByIDFn func(context.Context, string) (*contract.Policy, error)
	findByARFn func(context.Context, string, string) (*contract.Policy, error)
	listFn     func(context.Context) ([]contract.Policy, error)
	insertFn   func(context.Context, contract.Policy) (*contract.Policy, error)
	updateFn   func(context.Context, string, UpdateParams) (*contract.Policy, error)
	deleteFn   func(context.Context, string) (bool, error)
}

func (f *fakeStore) FindByID(ctx context.Context, id string) (*contract.Policy, error) {
	return f.findByIDFn(ctx, id)
}
func (f *fakeStore) FindByActionResource(ctx context.Context, p, rt string) (*contract.Policy, error) {
	return f.findByARFn(ctx, p, rt)
}
func (f *fakeStore) ListPolicies(ctx context.Context) ([]contract.Policy, error) {
	return f.listFn(ctx)
}
func (f *fakeStore) InsertPolicy(ctx context.Context, p contract.Policy) (*contract.Policy, error) {
	return f.insertFn(ctx, p)
}
func (f *fakeStore) UpdatePolicy(ctx context.Context, id string, patch UpdateParams) (*contract.Policy, error) {
	return f.updateFn(ctx, id, patch)
}
func (f *fakeStore) DeletePolicy(ctx context.Context, id string) (bool, error) {
	return f.deleteFn(ctx, id)
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

// modelWithFixture는 docFolderTeamIR을 담은 현재 버전을 돌려주는 ModelReader다.
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

func newRouter(deps Deps) *chi.Mux {
	r := chi.NewRouter()
	r.NotFound(httpx.WriteHonoNotFound)
	Mount(r, deps)
	return r
}

func adminDeps(store Store, mr ModelReader, rec Recorder) Deps {
	return Deps{Store: store, Model: mr, Recorder: rec, Auth: stubAuth{p: httpx.Principal{Role: httpx.RoleAdmin}}}
}

func do(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func nilPolicy(context.Context, string) (*contract.Policy, error)           { return nil, nil }
func nilPolicyAR(context.Context, string, string) (*contract.Policy, error) { return nil, nil }

// ---- create ----

func TestCreate(t *testing.T) {
	t.Run("missing fields -> 422", func(t *testing.T) {
		deps := adminDeps(&fakeStore{}, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"x"}`)
		if w.Code != 422 || w.Body.String() != `{"error":"id, permission, resourceType are required"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("invalid json -> 422", func(t *testing.T) {
		deps := adminDeps(&fakeStore{}, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `not json`)
		if w.Code != 422 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("bad slug -> 422", func(t *testing.T) {
		store := &fakeStore{findByIDFn: nilPolicy, findByARFn: nilPolicyAR}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"Bad_Slug","permission":"read","resourceType":"document"}`)
		if w.Code != 422 || w.Body.String() != `{"error":"id must be a slug matching ^[a-z0-9-]+$"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("dup id -> 409", func(t *testing.T) {
		store := &fakeStore{findByIDFn: func(context.Context, string) (*contract.Policy, error) {
			return &contract.Policy{ID: "doc-read"}, nil
		}}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"doc-read","permission":"read","resourceType":"document"}`)
		if w.Code != 409 || w.Body.String() != `{"error":"policy id \"doc-read\" already exists"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("dup (perm,resource) -> 409", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: nilPolicy,
			findByARFn: func(context.Context, string, string) (*contract.Policy, error) {
				return &contract.Policy{ID: "other"}, nil
			},
		}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"doc-read","permission":"read","resourceType":"document"}`)
		if w.Code != 409 || w.Body.String() != `{"error":"a policy for (read, document) already exists"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("no model -> 422", func(t *testing.T) {
		store := &fakeStore{findByIDFn: nilPolicy, findByARFn: nilPolicyAR}
		mr := fakeModel{fn: func(context.Context) (*model.Version, error) { return nil, nil }}
		deps := adminDeps(store, mr, &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"doc-read","permission":"read","resourceType":"document"}`)
		if w.Code != 422 || w.Body.String() != `{"error":"no model published yet; publish a model first"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("unknown resource type -> 422", func(t *testing.T) {
		store := &fakeStore{findByIDFn: nilPolicy, findByARFn: nilPolicyAR}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"g","permission":"read","resourceType":"ghost"}`)
		if w.Code != 422 || w.Body.String() != `{"error":"current model has no resource type \"ghost\""}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("unknown permission -> 422", func(t *testing.T) {
		store := &fakeStore{findByIDFn: nilPolicy, findByARFn: nilPolicyAR}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"g","permission":"delete","resourceType":"document"}`)
		if w.Code != 422 || w.Body.String() != `{"error":"\"document\" has no permission \"can_delete\" in the current model"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("success -> 201", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: nilPolicy,
			findByARFn: nilPolicyAR,
			insertFn: func(_ context.Context, p contract.Policy) (*contract.Policy, error) {
				return &p, nil
			},
		}
		rec := &fakeRecorder{}
		deps := adminDeps(store, modelWithFixture(t), rec)
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"doc-read","permission":"read","resourceType":"document","description":"can read docs"}`)
		want := `{"policy":{"id":"doc-read","permission":"read","resourceType":"document","description":"can read docs"}}`
		if w.Code != 201 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
		if !rec.has("policy.create") {
			t.Error("expected policy.create audit")
		}
	})
	t.Run("23505 backstop -> 409", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: nilPolicy,
			findByARFn: nilPolicyAR,
			insertFn: func(context.Context, contract.Policy) (*contract.Policy, error) {
				return nil, &pgconn.PgError{Code: "23505"}
			},
		}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"doc-read","permission":"read","resourceType":"document"}`)
		if w.Code != 409 || w.Body.String() != `{"error":"policy already exists (id or (permission, resourceType))"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("insert other error -> 500", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: nilPolicy,
			findByARFn: nilPolicyAR,
			insertFn:   func(context.Context, contract.Policy) (*contract.Policy, error) { return nil, errors.New("boom") },
		}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"doc-read","permission":"read","resourceType":"document"}`)
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("store lookup error -> 500", func(t *testing.T) {
		store := &fakeStore{findByIDFn: func(context.Context, string) (*contract.Policy, error) { return nil, errors.New("boom") }}
		deps := adminDeps(store, modelWithFixture(t), &fakeRecorder{})
		w := do(t, newRouter(deps), http.MethodPost, "/policies", `{"id":"doc-read","permission":"read","resourceType":"document"}`)
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// ---- list / get ----

func TestListAndGet(t *testing.T) {
	desc := "d"
	pol := contract.Policy{ID: "p1", Permission: "read", ResourceType: "document", Description: &desc}
	t.Run("list", func(t *testing.T) {
		store := &fakeStore{listFn: func(context.Context) ([]contract.Policy, error) { return []contract.Policy{pol}, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil)), http.MethodGet, "/policies", "")
		want := `{"policies":[{"id":"p1","permission":"read","resourceType":"document","description":"d"}]}`
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
	})
	t.Run("list empty", func(t *testing.T) {
		store := &fakeStore{listFn: func(context.Context) ([]contract.Policy, error) { return []contract.Policy{}, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil)), http.MethodGet, "/policies", "")
		if w.Body.String() != `{"policies":[]}` {
			t.Fatalf("body=%s", w.Body.String())
		}
	})
	t.Run("list error", func(t *testing.T) {
		store := &fakeStore{listFn: func(context.Context) ([]contract.Policy, error) { return nil, errors.New("boom") }}
		w := do(t, newRouter(adminDeps(store, nil, nil)), http.MethodGet, "/policies", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("get found", func(t *testing.T) {
		store := &fakeStore{findByIDFn: func(context.Context, string) (*contract.Policy, error) { return &pol, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil)), http.MethodGet, "/policies/p1", "")
		if w.Code != 200 || w.Body.String() != `{"policy":{"id":"p1","permission":"read","resourceType":"document","description":"d"}}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("get not found", func(t *testing.T) {
		store := &fakeStore{findByIDFn: nilPolicy}
		w := do(t, newRouter(adminDeps(store, nil, nil)), http.MethodGet, "/policies/nope", "")
		if w.Code != 404 || w.Body.String() != `{"error":"policy not found"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("get error", func(t *testing.T) {
		store := &fakeStore{findByIDFn: func(context.Context, string) (*contract.Policy, error) { return nil, errors.New("boom") }}
		w := do(t, newRouter(adminDeps(store, nil, nil)), http.MethodGet, "/policies/p1", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// ---- update ----

func TestUpdate(t *testing.T) {
	existing := &contract.Policy{ID: "p1", Permission: "read", ResourceType: "document"}
	t.Run("precheck 404", func(t *testing.T) {
		store := &fakeStore{findByIDFn: nilPolicy}
		w := do(t, newRouter(adminDeps(store, modelWithFixture(t), &fakeRecorder{})), http.MethodPut, "/policies/p1", `{}`)
		if w.Code != 404 || w.Body.String() != `{"error":"policy not found"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("precheck error -> 500", func(t *testing.T) {
		store := &fakeStore{findByIDFn: func(context.Context, string) (*contract.Policy, error) { return nil, errors.New("boom") }}
		w := do(t, newRouter(adminDeps(store, modelWithFixture(t), &fakeRecorder{})), http.MethodPut, "/policies/p1", `{}`)
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("clash -> 409", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: func(context.Context, string) (*contract.Policy, error) { return existing, nil },
			findByARFn: func(context.Context, string, string) (*contract.Policy, error) {
				return &contract.Policy{ID: "other"}, nil
			},
		}
		w := do(t, newRouter(adminDeps(store, modelWithFixture(t), &fakeRecorder{})), http.MethodPut, "/policies/p1", `{"permission":"read","resourceType":"document"}`)
		if w.Code != 409 || w.Body.String() != `{"error":"a policy for (read, document) already exists"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("target 422", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: func(context.Context, string) (*contract.Policy, error) { return existing, nil },
			findByARFn: nilPolicyAR,
		}
		w := do(t, newRouter(adminDeps(store, modelWithFixture(t), &fakeRecorder{})), http.MethodPut, "/policies/p1", `{"permission":"delete"}`)
		if w.Code != 422 || w.Body.String() != `{"error":"\"document\" has no permission \"can_delete\" in the current model"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("success", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: func(context.Context, string) (*contract.Policy, error) { return existing, nil },
			findByARFn: nilPolicyAR,
			updateFn: func(_ context.Context, id string, patch UpdateParams) (*contract.Policy, error) {
				return &contract.Policy{ID: id, Permission: patch.Permission, ResourceType: patch.ResourceType, Description: patch.Description}, nil
			},
		}
		rec := &fakeRecorder{}
		w := do(t, newRouter(adminDeps(store, modelWithFixture(t), rec)), http.MethodPut, "/policies/p1", `{"description":"updated"}`)
		want := `{"policy":{"id":"p1","permission":"read","resourceType":"document","description":"updated"}}`
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
		if !rec.has("policy.update") {
			t.Error("expected policy.update audit")
		}
	})
	t.Run("update 23505 -> 409", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: func(context.Context, string) (*contract.Policy, error) { return existing, nil },
			findByARFn: nilPolicyAR,
			updateFn: func(context.Context, string, UpdateParams) (*contract.Policy, error) {
				return nil, &pgconn.PgError{Code: "23505"}
			},
		}
		w := do(t, newRouter(adminDeps(store, modelWithFixture(t), &fakeRecorder{})), http.MethodPut, "/policies/p1", `{}`)
		if w.Code != 409 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("update race gone -> 422", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: func(context.Context, string) (*contract.Policy, error) { return existing, nil },
			findByARFn: nilPolicyAR,
			updateFn:   func(context.Context, string, UpdateParams) (*contract.Policy, error) { return nil, nil },
		}
		w := do(t, newRouter(adminDeps(store, modelWithFixture(t), &fakeRecorder{})), http.MethodPut, "/policies/p1", `{}`)
		if w.Code != 422 || w.Body.String() != `{"error":"policy \"p1\" not found"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
}

// ---- delete ----

func TestDelete(t *testing.T) {
	t.Run("deleted -> 204", func(t *testing.T) {
		store := &fakeStore{deleteFn: func(context.Context, string) (bool, error) { return true, nil }}
		rec := &fakeRecorder{}
		w := do(t, newRouter(adminDeps(store, nil, rec)), http.MethodDelete, "/policies/p1", "")
		if w.Code != 204 || w.Body.Len() != 0 {
			t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
		}
		if !rec.has("policy.delete") {
			t.Error("expected policy.delete audit")
		}
	})
	t.Run("not found -> 404", func(t *testing.T) {
		store := &fakeStore{deleteFn: func(context.Context, string) (bool, error) { return false, nil }}
		w := do(t, newRouter(adminDeps(store, nil, &fakeRecorder{})), http.MethodDelete, "/policies/p1", "")
		if w.Code != 404 || w.Body.String() != `{"error":"policy not found"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("error -> 500", func(t *testing.T) {
		store := &fakeStore{deleteFn: func(context.Context, string) (bool, error) { return false, errors.New("boom") }}
		w := do(t, newRouter(adminDeps(store, nil, &fakeRecorder{})), http.MethodDelete, "/policies/p1", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

func TestNew(t *testing.T) {
	d := New(nil, fakeModel{}, &fakeRecorder{}, stubAuth{})
	if d.Store == nil || d.Model == nil || d.Recorder == nil {
		t.Fatalf("incomplete deps: %+v", d)
	}
}

func TestStringField(t *testing.T) {
	m := map[string]any{"a": "x", "b": 3}
	if got := stringField(m, "a"); got == nil || *got != "x" {
		t.Errorf("a -> %v", got)
	}
	if stringField(m, "b") != nil {
		t.Error("non-string should be nil")
	}
	if stringField(m, "missing") != nil {
		t.Error("missing should be nil")
	}
}

func TestUpdate_assertModelError(t *testing.T) {
	// editPolicy 내 assertModelHasTarget이 raw error를 전파 → Hono 500.
	existing := &contract.Policy{ID: "p1", Permission: "read", ResourceType: "document"}
	calls := 0
	mr := fakeModel{fn: func(context.Context) (*model.Version, error) {
		calls++
		return nil, errors.New("model down")
	}}
	store := &fakeStore{
		findByIDFn: func(context.Context, string) (*contract.Policy, error) { return existing, nil },
		findByARFn: nilPolicyAR,
	}
	w := do(t, newRouter(adminDeps(store, mr, &fakeRecorder{})), http.MethodPut, "/policies/p1", `{}`)
	if w.Code != 500 || calls == 0 {
		t.Fatalf("code=%d calls=%d", w.Code, calls)
	}
	_ = json.Marshal
}
