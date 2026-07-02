package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/modules/auth"
)

type fakeRepo struct {
	token   *auth.ServiceToken
	findErr error
	touched []string
}

func (f *fakeRepo) FindActiveByHash(_ context.Context, _ string) (*auth.ServiceToken, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.token, nil
}
func (f *fakeRepo) TouchLastUsed(_ context.Context, id string) error {
	f.touched = append(f.touched, id)
	return nil
}

// syncTouch는 디스패치를 동기화해 touch 호출을 결정적으로 관찰한다.
func syncAuth(adminToken string, repo ServiceTokenRepo) *TokenAuthenticator {
	a := NewTokenAuthenticator(adminToken, repo)
	a.touch = func(fn func()) { fn() }
	return a
}

func TestParseBearer(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer abc", "abc"},
		{"bearer abc", "abc"},
		{"BEARER   xyz  ", "xyz"},
		{"  Bearer tok  ", "tok"},
		{"", ""},
		{"Bearer", ""},
		{"Bearer ", ""},
		{"Basic abc", ""},
		{"token abc", ""},
	}
	for _, tc := range tests {
		if got := parseBearer(tc.header); got != tc.want {
			t.Errorf("parseBearer(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abcd", "abcd") {
		t.Error("equal strings should compare true")
	}
	if constantTimeEqual("abcd", "abce") {
		t.Error("different strings should compare false")
	}
	if constantTimeEqual("abc", "abcd") {
		t.Error("different lengths should compare false")
	}
}

func TestAuthenticate(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		a := syncAuth("admintok", &fakeRepo{})
		_, err := a.Authenticate(context.Background(), "")
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
	})
	t.Run("admin match", func(t *testing.T) {
		a := syncAuth("admintok", &fakeRepo{})
		p, err := a.Authenticate(context.Background(), "Bearer admintok")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if p.Role != RoleAdmin {
			t.Fatalf("role = %q, want admin", p.Role)
		}
	})
	t.Run("empty admin token never matches", func(t *testing.T) {
		repo := &fakeRepo{} // 토큰 없음.
		a := syncAuth("", repo)
		_, err := a.Authenticate(context.Background(), "Bearer anything")
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("empty admin token must fall through to repo (miss -> 401), got %v", err)
		}
	})
	t.Run("service token found touches last used", func(t *testing.T) {
		repo := &fakeRepo{token: &auth.ServiceToken{ID: "tok-1"}}
		a := syncAuth("admintok", repo)
		p, err := a.Authenticate(context.Background(), "Bearer svc-plain")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if p.Role != RoleService || p.TokenID != "tok-1" {
			t.Fatalf("principal = %+v", p)
		}
		if len(repo.touched) != 1 || repo.touched[0] != "tok-1" {
			t.Fatalf("touched = %v, want [tok-1]", repo.touched)
		}
	})
	t.Run("service token not found", func(t *testing.T) {
		a := syncAuth("admintok", &fakeRepo{token: nil})
		_, err := a.Authenticate(context.Background(), "Bearer nope")
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
	})
	t.Run("infra error propagates (not 401)", func(t *testing.T) {
		boom := errors.New("db down")
		a := syncAuth("admintok", &fakeRepo{findErr: boom})
		_, err := a.Authenticate(context.Background(), "Bearer x")
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
		if errors.Is(err, ErrUnauthorized) {
			t.Fatal("infra error must not be ErrUnauthorized")
		}
	})
	t.Run("default async touch does not block", func(t *testing.T) {
		// 기본 생성자(goroutine 디스패치)로 touch 경로가 호출되어도 결정에 영향 없음.
		repo := &fakeRepo{token: &auth.ServiceToken{ID: "async-1"}}
		a := NewTokenAuthenticator("admintok", repo)
		p, err := a.Authenticate(context.Background(), "Bearer svc")
		if err != nil || p.Role != RoleService {
			t.Fatalf("p=%+v err=%v", p, err)
		}
	})
}

// stubAuth는 고정 결과를 반환하는 Authenticator다(RequireRole 테스트용).
type stubAuth struct {
	principal Principal
	err       error
}

func (s stubAuth) Authenticate(context.Context, string) (Principal, error) {
	return s.principal, s.err
}

func TestRequireRole(t *testing.T) {
	okHandler := func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFromContext(r.Context())
		if !ok {
			t.Error("principal missing from context")
		}
		WriteJSON(w, 200, map[string]string{"role": string(p.Role)})
	}

	t.Run("unauthorized -> 401", func(t *testing.T) {
		h := RequireRole(stubAuth{err: ErrUnauthorized}, RoleAdmin)(http.HandlerFunc(okHandler))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != 401 || w.Body.String() != `{"error":"unauthorized"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("infra error -> 500", func(t *testing.T) {
		h := RequireRole(stubAuth{err: errors.New("db down")}, RoleAdmin)(http.HandlerFunc(okHandler))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != 500 || w.Body.String() != "Internal Server Error" {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("role not allowed -> 403", func(t *testing.T) {
		h := RequireRole(stubAuth{principal: Principal{Role: RoleService}}, RoleAdmin)(http.HandlerFunc(okHandler))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != 403 || w.Body.String() != `{"error":"forbidden"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("allowed sets principal and calls next", func(t *testing.T) {
		h := RequireRole(stubAuth{principal: Principal{Role: RoleAdmin}}, RoleAdmin, RoleService)(http.HandlerFunc(okHandler))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if w.Code != 200 || w.Body.String() != `{"role":"admin"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestPrincipalContext(t *testing.T) {
	if _, ok := PrincipalFromContext(context.Background()); ok {
		t.Error("empty context should have no principal")
	}
	ctx := ContextWithPrincipal(context.Background(), Principal{Role: RoleAdmin, TokenID: "t"})
	p, ok := PrincipalFromContext(ctx)
	if !ok || p.Role != RoleAdmin || p.TokenID != "t" {
		t.Fatalf("round trip failed: %+v ok=%v", p, ok)
	}
}
