package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/modules/auth"
	"github.com/go-chi/chi/v5"
)

// ── fakes ──

type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, header string) (httpx.Principal, error) {
	switch header {
	case "Bearer admin":
		return httpx.Principal{Role: httpx.RoleAdmin}, nil
	case "Bearer service":
		return httpx.Principal{Role: httpx.RoleService, TokenID: "tok1"}, nil
	case "Bearer boom":
		return httpx.Principal{}, context.DeadlineExceeded // infra error, not ErrUnauthorized
	default:
		return httpx.Principal{}, httpx.ErrUnauthorized
	}
}

type recCall struct {
	action string
	actor  string
	data   map[string]any
}

type fakeRecorder struct{ calls []recCall }

func (f *fakeRecorder) Record(action string, data map[string]any, actor string) {
	f.calls = append(f.calls, recCall{action, actor, data})
}

type fakeRepo struct {
	createFn func(context.Context, string, string) (*auth.ServiceToken, error)
	listFn   func(context.Context) ([]auth.ServiceToken, error)
	revokeFn func(context.Context, string) (bool, error)
}

func (f *fakeRepo) Create(ctx context.Context, name, hash string) (*auth.ServiceToken, error) {
	return f.createFn(ctx, name, hash)
}
func (f *fakeRepo) List(ctx context.Context) ([]auth.ServiceToken, error) { return f.listFn(ctx) }
func (f *fakeRepo) Revoke(ctx context.Context, id string) (bool, error)   { return f.revokeFn(ctx, id) }

func actorFn(ctx context.Context) string {
	p, _ := httpx.PrincipalFromContext(ctx)
	if p.Role == httpx.RoleAdmin {
		return "admin"
	}
	if p.TokenID != "" {
		return "service:" + p.TokenID
	}
	return "service"
}

func newRouter(repo auth.TokenRepo, rec auth.Recorder) *chi.Mux {
	r := chi.NewRouter()
	r.NotFound(httpx.WriteHonoNotFound)
	auth.Mount(r, auth.Deps{
		Repo:         repo,
		Recorder:     rec,
		RequireAdmin: httpx.RequireRole(fakeAuth{}, httpx.RoleAdmin),
		Actor:        actorFn,
	})
	return r
}

func do(r chi.Router, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func adminHdr() map[string]string { return map[string]string{"Authorization": "Bearer admin"} }

const uuid1 = "11111111-1111-1111-1111-111111111111"

// ── auth guard ──

func TestTokens_AuthGuard(t *testing.T) {
	repo := &fakeRepo{listFn: func(context.Context) ([]auth.ServiceToken, error) { return nil, nil }}
	r := newRouter(repo, &fakeRecorder{})

	t.Run("401 without token", func(t *testing.T) {
		w := do(r, "GET", "/tokens", "", nil)
		if w.Code != http.StatusUnauthorized || w.Body.String() != `{"error":"unauthorized"}` {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("403 service token", func(t *testing.T) {
		w := do(r, "GET", "/tokens", "", map[string]string{"Authorization": "Bearer service"})
		if w.Code != http.StatusForbidden || w.Body.String() != `{"error":"forbidden"}` {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("500 auth infra error (not masked as 401)", func(t *testing.T) {
		w := do(r, "GET", "/tokens", "", map[string]string{"Authorization": "Bearer boom"})
		if w.Code != http.StatusInternalServerError || w.Body.String() != "Internal Server Error" {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
}

// ── POST /tokens ──

func TestPostToken(t *testing.T) {
	t.Run("400 name required (variants)", func(t *testing.T) {
		repo := &fakeRepo{}
		r := newRouter(repo, &fakeRecorder{})
		for _, body := range []string{`{}`, `{"name":"   "}`, `{"name":123}`, `not-json`, `"astring"`, `[]`} {
			w := do(r, "POST", "/tokens", body, adminHdr())
			if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"name is required"}` {
				t.Errorf("body %q → %d %s", body, w.Code, w.Body.String())
			}
		}
	})

	t.Run("201 issues token once + audit", func(t *testing.T) {
		var gotHash string
		repo := &fakeRepo{createFn: func(_ context.Context, name, hash string) (*auth.ServiceToken, error) {
			gotHash = hash
			return &auth.ServiceToken{ID: "t1", Name: name}, nil
		}}
		rec := &fakeRecorder{}
		w := do(newRouter(repo, rec), "POST", "/tokens", `{"name":"  ci  "}`, adminHdr())
		if w.Code != http.StatusCreated {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		var resp struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ID != "t1" || resp.Name != "ci" || resp.Token == "" {
			t.Fatalf("resp = %+v", resp)
		}
		// plaintext returned must hash to the stored hash (one-time exposure).
		if auth.Sha256Hex(resp.Token) != gotHash {
			t.Errorf("returned token does not match stored hash")
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "token.create" || rec.calls[0].actor != "admin" {
			t.Fatalf("audit = %+v", rec.calls)
		}
		if rec.calls[0].data["id"] != "t1" || rec.calls[0].data["name"] != "ci" {
			t.Errorf("audit data = %+v", rec.calls[0].data)
		}
	})

	t.Run("500 on repo error", func(t *testing.T) {
		repo := &fakeRepo{createFn: func(context.Context, string, string) (*auth.ServiceToken, error) {
			return nil, context.DeadlineExceeded
		}}
		w := do(newRouter(repo, &fakeRecorder{}), "POST", "/tokens", `{"name":"x"}`, adminHdr())
		if w.Code != http.StatusInternalServerError || w.Body.String() != "Internal Server Error" {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
}

// ── GET /tokens ──

func TestListTokens(t *testing.T) {
	created := time.Date(2026, 7, 2, 12, 0, 0, 123_000_000, time.UTC)
	created2 := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	lastUsed := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	repo := &fakeRepo{listFn: func(context.Context) ([]auth.ServiceToken, error) {
		return []auth.ServiceToken{
			{ID: "t1", Name: "a", TokenHash: "secret-hash", CreatedAt: created},
			{ID: "t2", Name: "b", CreatedAt: created2, LastUsedAt: &lastUsed, RevokedAt: &lastUsed},
		}, nil
	}}
	w := do(newRouter(repo, &fakeRecorder{}), "GET", "/tokens", "", adminHdr())
	want := `{"tokens":[` +
		`{"id":"t1","name":"a","createdAt":"2026-07-02T12:00:00.123Z","lastUsedAt":null,"revoked":false},` +
		`{"id":"t2","name":"b","createdAt":"2026-07-02T12:00:00.000Z","lastUsedAt":"2026-07-03T00:00:00.000Z","revoked":true}` +
		`]}`
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Fatalf("got %d %s\nwant %s", w.Code, w.Body.String(), want)
	}
	if strings.Contains(w.Body.String(), "secret-hash") {
		t.Error("hash must never be serialized")
	}
}

func TestListTokens_Error(t *testing.T) {
	repo := &fakeRepo{listFn: func(context.Context) ([]auth.ServiceToken, error) { return nil, context.DeadlineExceeded }}
	w := do(newRouter(repo, &fakeRecorder{}), "GET", "/tokens", "", adminHdr())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.Code)
	}
}

// ── DELETE /tokens/:id ──

func TestDeleteToken(t *testing.T) {
	t.Run("404 malformed uuid", func(t *testing.T) {
		// malformed uuid는 라우트 선필터가 아니라 repo의 22P02 매핑이 (false,nil)을 돌린다(§4.4-2).
		repo := &fakeRepo{revokeFn: func(context.Context, string) (bool, error) { return false, nil }}
		w := do(newRouter(repo, &fakeRecorder{}), "DELETE", "/tokens/not-a-uuid", "", adminHdr())
		if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"token not found"}` {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("404 not found", func(t *testing.T) {
		repo := &fakeRepo{revokeFn: func(context.Context, string) (bool, error) { return false, nil }}
		w := do(newRouter(repo, &fakeRecorder{}), "DELETE", "/tokens/"+uuid1, "", adminHdr())
		if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"token not found"}` {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("500 repo error", func(t *testing.T) {
		repo := &fakeRepo{revokeFn: func(context.Context, string) (bool, error) { return false, context.DeadlineExceeded }}
		w := do(newRouter(repo, &fakeRecorder{}), "DELETE", "/tokens/"+uuid1, "", adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
	t.Run("204 success + audit", func(t *testing.T) {
		repo := &fakeRepo{revokeFn: func(context.Context, string) (bool, error) { return true, nil }}
		rec := &fakeRecorder{}
		w := do(newRouter(repo, rec), "DELETE", "/tokens/"+uuid1, "", adminHdr())
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("got %d body=%q", w.Code, w.Body.String())
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "token.revoke" || rec.calls[0].data["id"] != uuid1 {
			t.Fatalf("audit = %+v", rec.calls)
		}
	})
}
