package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/compiler"
	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
	"github.com/go-chi/chi/v5"
	fga "github.com/openfga/go-sdk"
)

// ---- fakes ----

type stubAuth struct {
	p   httpx.Principal
	err error
}

func (s stubAuth) Authenticate(context.Context, string) (httpx.Principal, error) { return s.p, s.err }

type fakeStore struct {
	currentFn func(context.Context) (*Version, error)
	listFn    func(context.Context) ([]Version, error)
	getFn     func(context.Context, string) (*Version, error)
	insertFn  func(context.Context, InsertParams) (*PublishedVersion, error)
}

func (f *fakeStore) CurrentVersion(ctx context.Context) (*Version, error) {
	return f.currentFn(ctx)
}
func (f *fakeStore) ListVersions(ctx context.Context) ([]Version, error) { return f.listFn(ctx) }
func (f *fakeStore) GetVersion(ctx context.Context, id string) (*Version, error) {
	return f.getFn(ctx, id)
}
func (f *fakeStore) InsertVersion(ctx context.Context, in InsertParams) (*PublishedVersion, error) {
	return f.insertFn(ctx, in)
}

type fakeGateway struct {
	modelID string
	err     error
}

func (f *fakeGateway) WriteAuthorizationModel(_ context.Context, _ fga.WriteAuthorizationModelRequest) (string, error) {
	return f.modelID, f.err
}

type fakeCompiler struct {
	dsl       string
	modelJSON []byte
	err       error
}

func (f fakeCompiler) Compile(_ *contract.ModelIR) (string, []byte, error) {
	return f.dsl, f.modelJSON, f.err
}

type recorded struct {
	action string
	data   map[string]any
	actor  string
}
type fakeRecorder struct{ records []recorded }

func (f *fakeRecorder) Record(action string, data map[string]any, actor string) {
	f.records = append(f.records, recorded{action, data, actor})
}
func (f *fakeRecorder) has(action string) bool {
	for _, r := range f.records {
		if r.action == action {
			return true
		}
	}
	return false
}

func newRouter(deps Deps) *chi.Mux {
	r := chi.NewRouter()
	r.NotFound(httpx.WriteHonoNotFound)
	Mount(r, deps)
	return r
}

func adminDeps(store Store, gw Gateway, comp Compiler, rec Recorder) Deps {
	return Deps{Store: store, Gateway: gw, Compiler: comp, Recorder: rec, Auth: stubAuth{p: httpx.Principal{Role: httpx.RoleAdmin}}}
}

func do(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---- publish ----

func fixtureIRBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(testutil.RepoPath("packages", "shared", "src", "__fixtures__", "doc-folder-team.ir.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func okStore() *fakeStore {
	return &fakeStore{
		currentFn: func(context.Context) (*Version, error) { return nil, nil },
		listFn:    func(context.Context) ([]Version, error) { return nil, nil },
		getFn:     func(context.Context, string) (*Version, error) { return nil, nil },
		insertFn: func(_ context.Context, in InsertParams) (*PublishedVersion, error) {
			return &PublishedVersion{ID: "v1", AuthorizationModelID: in.AuthorizationModelID, CreatedAt: time.Date(2026, 7, 2, 3, 4, 5, 6_000_000, time.UTC)}, nil
		},
	}
}

func TestPublish_success(t *testing.T) {
	rec := &fakeRecorder{}
	store := okStore()
	deps := adminDeps(store, &fakeGateway{modelID: "model-1"}, DefaultCompiler(), rec)
	body := `{"ir":` + string(fixtureIRBytes(t)) + `,"note":"first"}`
	w := do(t, newRouter(deps), http.MethodPost, "/model", body)
	if w.Code != 201 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	want := `{"version":{"id":"v1","authorizationModelId":"model-1","createdAt":"2026-07-02T03:04:05.006Z"}}`
	if w.Body.String() != want {
		t.Fatalf("body=%s want=%s", w.Body.String(), want)
	}
	if !rec.has("model.publish") {
		t.Error("expected model.publish audit")
	}
}

func TestPublish_noteNonStringIgnored(t *testing.T) {
	var captured InsertParams
	store := okStore()
	store.insertFn = func(_ context.Context, in InsertParams) (*PublishedVersion, error) {
		captured = in
		return &PublishedVersion{ID: "v1", AuthorizationModelID: in.AuthorizationModelID, CreatedAt: time.Unix(0, 0).UTC()}, nil
	}
	deps := adminDeps(store, &fakeGateway{modelID: "m"}, DefaultCompiler(), &fakeRecorder{})
	body := `{"ir":` + string(fixtureIRBytes(t)) + `,"note":42}`
	if w := do(t, newRouter(deps), http.MethodPost, "/model", body); w.Code != 201 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if captured.Note != nil {
		t.Errorf("non-string note should be nil, got %v", *captured.Note)
	}
	// TS는 zod parsed.data를 저장한다 — 디코드된 IR의 canonical 직렬화가 저장돼야 한다
	// (미지 키 제거·숫자 정규화; LFGA-26 리뷰 반영).
	var decoded map[string]any
	if err := json.Unmarshal(fixtureIRBytes(t), &decoded); err != nil {
		t.Fatal(err)
	}
	ir, issues := contract.DecodeModelIR(fixtureIRBytes(t))
	if len(issues) > 0 {
		t.Fatalf("fixture must decode: %v", issues)
	}
	want, err := jsutil.MarshalJSON(ir)
	if err != nil {
		t.Fatal(err)
	}
	if string(captured.IRJSON) != string(want) {
		t.Errorf("IRJSON should be the canonical serialization of the decoded IR, got %s", captured.IRJSON)
	}
}

func TestPublish_shapeError(t *testing.T) {
	deps := adminDeps(okStore(), &fakeGateway{}, fakeCompiler{}, &fakeRecorder{})
	// ir 부재 → invalid IR shape. §4.4-1 편차의 issues 형태({path,message})까지 전량 고정한다(리뷰 #21b).
	w := do(t, newRouter(deps), http.MethodPost, "/model", `{"note":"x"}`)
	if w.Code != 422 || w.Body.String() != `{"error":"invalid IR shape","issues":[{"path":"","message":"invalid JSON: unexpected end of JSON input"}]}` {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Body.String(), `{"error":"invalid IR shape","issues":[`) {
		t.Fatalf("body=%s", w.Body.String())
	}
	// wrong schemaVersion literal → shape issue.
	w2 := do(t, newRouter(deps), http.MethodPost, "/model", `{"ir":{"schemaVersion":"9.9","groups":[],"resources":[]}}`)
	if w2.Code != 422 || !strings.Contains(w2.Body.String(), "invalid IR shape") {
		t.Fatalf("code=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestPublish_validationError(t *testing.T) {
	deps := adminDeps(okStore(), &fakeGateway{}, fakeCompiler{err: errors.New("should not reach")}, &fakeRecorder{})
	// shape-valid but semantically invalid: permission granted by unknown role.
	ir := `{"ir":{"schemaVersion":"1.1","groups":[],"resources":[{"name":"doc","parents":[],"roles":[],"permissions":[{"name":"read","grantedByRoles":["ghost"],"inheritFromParents":[]}]}]}}`
	w := do(t, newRouter(deps), http.MethodPost, "/model", ir)
	if w.Code != 422 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Body.String(), `{"error":"publish failed (422)","detail":{"validation":[`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestPublish_compileError(t *testing.T) {
	comp := fakeCompiler{err: &compiler.CompileError{Reason: "JSON_TRANSFORM_FAILED", Detail: "boom"}}
	deps := adminDeps(okStore(), &fakeGateway{}, comp, &fakeRecorder{})
	body := `{"ir":` + string(fixtureIRBytes(t)) + `}`
	w := do(t, newRouter(deps), http.MethodPost, "/model", body)
	want := `{"error":"publish failed (422)","detail":{"compile":"JSON_TRANSFORM_FAILED","detail":"boom"}}`
	if w.Code != 422 || w.Body.String() != want {
		t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
	}
}

func TestPublish_compileUnmarshalDefensive(t *testing.T) {
	comp := fakeCompiler{dsl: "d", modelJSON: []byte("{not json")}
	deps := adminDeps(okStore(), &fakeGateway{}, comp, &fakeRecorder{})
	body := `{"ir":` + string(fixtureIRBytes(t)) + `}`
	w := do(t, newRouter(deps), http.MethodPost, "/model", body)
	if w.Code != 500 || w.Body.String() != "Internal Server Error" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPublish_openfgaError(t *testing.T) {
	comp := fakeCompiler{dsl: "d", modelJSON: []byte(`{"schema_version":"1.1","type_definitions":[]}`)}
	deps := adminDeps(okStore(), &fakeGateway{err: errors.New("upstream down")}, comp, &fakeRecorder{})
	body := `{"ir":` + string(fixtureIRBytes(t)) + `}`
	w := do(t, newRouter(deps), http.MethodPost, "/model", body)
	want := `{"error":"publish failed (502)","detail":{"openfga":"upstream down"}}`
	if w.Code != 502 || w.Body.String() != want {
		t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
	}
}

func TestPublish_dbFailure(t *testing.T) {
	comp := fakeCompiler{dsl: "d", modelJSON: []byte(`{"schema_version":"1.1","type_definitions":[]}`)}
	store := okStore()
	store.insertFn = func(context.Context, InsertParams) (*PublishedVersion, error) {
		return nil, errors.New("db exploded")
	}
	rec := &fakeRecorder{}
	deps := adminDeps(store, &fakeGateway{modelID: "orphan-1"}, comp, rec)
	body := `{"ir":` + string(fixtureIRBytes(t)) + `}`
	w := do(t, newRouter(deps), http.MethodPost, "/model", body)
	want := `{"error":"publish failed (500)","detail":{"db":"db exploded","orphanModelId":"orphan-1"}}`
	if w.Code != 500 || w.Body.String() != want {
		t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
	}
	if !rec.has("model.publish.db_failure") {
		t.Error("expected db_failure audit")
	}
}

// ---- current ----

func TestCurrent(t *testing.T) {
	note := "hello"
	v := &Version{ID: "v1", AuthorizationModelID: "m1", IRJSON: []byte(`{"a":1}`), DSL: "model", Note: &note, CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 7_000_000, time.UTC)}
	t.Run("found", func(t *testing.T) {
		store := &fakeStore{currentFn: func(context.Context) (*Version, error) { return v, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/current", "")
		want := `{"version":{"id":"v1","authorizationModelId":"m1","createdAt":"2026-01-02T03:04:05.007Z","note":"hello"},"ir":{"a":1},"dsl":"model"}`
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
	})
	t.Run("none", func(t *testing.T) {
		store := &fakeStore{currentFn: func(context.Context) (*Version, error) { return nil, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/current", "")
		if w.Code != 404 || w.Body.String() != `{"error":"no model published yet"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("error", func(t *testing.T) {
		store := &fakeStore{currentFn: func(context.Context) (*Version, error) { return nil, errors.New("boom") }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/current", "")
		if w.Code != 500 || w.Body.String() != "Internal Server Error" {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
}

// ---- versions list ----

func TestListVersions(t *testing.T) {
	note := "n"
	rows := []Version{
		{ID: "v2", AuthorizationModelID: "m2", Note: &note, CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		{ID: "v1", AuthorizationModelID: "m1", Note: nil, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
	}
	t.Run("ok", func(t *testing.T) {
		store := &fakeStore{listFn: func(context.Context) ([]Version, error) { return rows, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/versions", "")
		want := `{"versions":[{"id":"v2","authorizationModelId":"m2","createdAt":"2026-01-02T00:00:00.000Z","note":"n"},{"id":"v1","authorizationModelId":"m1","createdAt":"2026-01-01T00:00:00.000Z","note":null}]}`
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
	})
	t.Run("empty", func(t *testing.T) {
		store := &fakeStore{listFn: func(context.Context) ([]Version, error) { return []Version{}, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/versions", "")
		if w.Body.String() != `{"versions":[]}` {
			t.Fatalf("body=%s", w.Body.String())
		}
	})
	t.Run("error", func(t *testing.T) {
		store := &fakeStore{listFn: func(context.Context) ([]Version, error) { return nil, errors.New("boom") }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/versions", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// ---- diff ----

func TestDiff(t *testing.T) {
	irBytes := fixtureIRBytes(t)
	base := &Version{ID: "a", IRJSON: irBytes}
	makeStore := func(fn func(context.Context, string) (*Version, error)) *fakeStore {
		return &fakeStore{getFn: fn}
	}
	t.Run("missing params", func(t *testing.T) {
		w := do(t, newRouter(adminDeps(makeStore(nil), nil, nil, nil)), http.MethodGet, "/model/diff?from=a", "")
		if w.Code != 400 || w.Body.String() != `{"error":"from and to query params required"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("not found", func(t *testing.T) {
		store := makeStore(func(_ context.Context, id string) (*Version, error) {
			if id == "a" {
				return base, nil
			}
			return nil, nil
		})
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/diff?from=a&to=b", "")
		if w.Code != 404 || w.Body.String() != `{"error":"version not found"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("from error", func(t *testing.T) {
		store := makeStore(func(context.Context, string) (*Version, error) { return nil, errors.New("boom") })
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/diff?from=a&to=b", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("to error", func(t *testing.T) {
		calls := 0
		store := makeStore(func(context.Context, string) (*Version, error) {
			calls++
			if calls == 1 {
				return base, nil
			}
			return nil, errors.New("boom")
		})
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/diff?from=a&to=b", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("bad ir", func(t *testing.T) {
		store := makeStore(func(context.Context, string) (*Version, error) {
			return &Version{IRJSON: []byte("not json")}, nil
		})
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/diff?from=a&to=b", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
	t.Run("ok", func(t *testing.T) {
		store := makeStore(func(context.Context, string) (*Version, error) { return base, nil })
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/diff?from=a&to=a", "")
		if w.Code != 200 || w.Body.String() != `{"changes":[]}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
}

// ---- getVersion ----

func TestGetVersion(t *testing.T) {
	v := &Version{ID: "v1", AuthorizationModelID: "m1", IRJSON: []byte(`{"x":true}`), DSL: "model", Note: nil, CreatedAt: time.Date(2026, 5, 5, 5, 5, 5, 0, time.UTC)}
	t.Run("found", func(t *testing.T) {
		store := &fakeStore{getFn: func(context.Context, string) (*Version, error) { return v, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/versions/11111111-1111-1111-1111-111111111111", "")
		want := `{"version":{"id":"v1","authorizationModelId":"m1","createdAt":"2026-05-05T05:05:05.000Z","note":null},"ir":{"x":true},"dsl":"model"}`
		if w.Code != 200 || w.Body.String() != want {
			t.Fatalf("code=%d body=%s want=%s", w.Code, w.Body.String(), want)
		}
	})
	t.Run("not found", func(t *testing.T) {
		store := &fakeStore{getFn: func(context.Context, string) (*Version, error) { return nil, nil }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/versions/whatever", "")
		if w.Code != 404 || w.Body.String() != `{"error":"version not found"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("error", func(t *testing.T) {
		store := &fakeStore{getFn: func(context.Context, string) (*Version, error) { return nil, errors.New("boom") }}
		w := do(t, newRouter(adminDeps(store, nil, nil, nil)), http.MethodGet, "/model/versions/x", "")
		if w.Code != 500 {
			t.Fatalf("code=%d", w.Code)
		}
	})
}

// ---- unit: createdByOf, GetVersion uuid guard, IR(), New ----

func TestCreatedByOf(t *testing.T) {
	if got := createdByOf(httpx.Principal{Role: httpx.RoleAdmin}); got != "admin" {
		t.Errorf("admin -> %q", got)
	}
	if got := createdByOf(httpx.Principal{Role: httpx.RoleService, TokenID: "t1"}); got != "token:t1" {
		t.Errorf("service -> %q", got)
	}
	if got := createdByOf(httpx.Principal{Role: httpx.RoleService}); got != "token:?" {
		t.Errorf("service-no-token -> %q", got)
	}
}

func TestRepoGetVersion_malformedUUID(t *testing.T) {
	// malformed uuid는 PG의 uuid 파서 거부(22P02)를 not-found로 매핑한다(§4.4-2).
	// PG 수용 형식(무하이픈/중괄호)은 정상 조회 경로를 타므로 regex 선필터를 쓰지 않는다.
	pool := newMigratedScratchDB(t)
	got, err := NewRepo(pool).GetVersion(context.Background(), "not-a-uuid")
	if err != nil || got != nil {
		t.Fatalf("got=%v err=%v, want nil,nil", got, err)
	}
}

func TestVersionIR_error(t *testing.T) {
	if _, err := (&Version{IRJSON: []byte("nope")}).IR(); err == nil {
		t.Error("invalid IRJSON should error")
	}
}

func TestNew(t *testing.T) {
	d := New(nil, &fakeGateway{}, &fakeRecorder{}, stubAuth{})
	if d.Store == nil || d.Compiler == nil || d.Gateway == nil || d.Recorder == nil {
		t.Fatalf("New produced incomplete deps: %+v", d)
	}
}

func TestPublish_dbFailureBodyShape(t *testing.T) {
	// json 정합성 확인용 스모크: detail 파싱.
	var parsed map[string]json.RawMessage
	comp := fakeCompiler{dsl: "d", modelJSON: []byte(`{"schema_version":"1.1","type_definitions":[]}`)}
	store := okStore()
	store.insertFn = func(context.Context, InsertParams) (*PublishedVersion, error) { return nil, errors.New("x") }
	deps := adminDeps(store, &fakeGateway{modelID: "m"}, comp, &fakeRecorder{})
	w := do(t, newRouter(deps), http.MethodPost, "/model", `{"ir":`+string(fixtureIRBytes(t))+`}`)
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not valid json: %v", err)
	}
}
