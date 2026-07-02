package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// genFakeRepo/genFakeRec는 GenerateToken 실패 분기 전용 스텁이다(Create까지 도달하지 않음).
type genFakeRepo struct{}

func (genFakeRepo) Create(context.Context, string, string) (*ServiceToken, error) { return nil, nil }
func (genFakeRepo) List(context.Context) ([]ServiceToken, error)                  { return nil, nil }
func (genFakeRepo) Revoke(context.Context, string) (bool, error)                  { return false, nil }

type genFakeRec struct{}

func (genFakeRec) Record(string, map[string]any, string) {}

// TestPostToken_GenerateTokenError는 토큰 생성(crypto/rand) 실패 → 500 분기를 커버한다.
// httpx import 순환을 피하려 pass-through 미들웨어와 고정 actor를 주입한다(package-internal 테스트).
func TestPostToken_GenerateTokenError(t *testing.T) {
	old := randReader
	defer func() { randReader = old }()
	randReader = errReader{}

	r := chi.NewRouter()
	Mount(r, Deps{
		Repo:         genFakeRepo{},
		Recorder:     genFakeRec{},
		RequireAdmin: func(next http.Handler) http.Handler { return next },
		Actor:        func(context.Context) string { return "admin" },
	})
	req := httptest.NewRequest("POST", "/tokens", strings.NewReader(`{"name":"x"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError || w.Body.String() != "Internal Server Error" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}

// TestWriteJSON_MarshalError는 직렬화 불가 값에 대한 500 폴백을 커버한다.
func TestWriteJSON_MarshalError(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, make(chan int)) // chan은 마샬 불가.
	if w.Code != http.StatusInternalServerError || w.Body.String() != `{"error":"internal error"}` {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}
