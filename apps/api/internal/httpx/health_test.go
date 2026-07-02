package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func staticBool(b bool) func(*http.Request) bool { return func(*http.Request) bool { return b } }

func TestHealth_allUp(t *testing.T) {
	h := Health{
		Version:    "0.0.0",
		DBPing:     staticBool(true),
		FGAPing:    staticBool(true),
		StoreReady: func() bool { return true },
	}
	w := httptest.NewRecorder()
	h.Handler()(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != 200 {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	// 필드 순서까지 TS와 동일해야 한다.
	want := `{"status":"ok","version":"0.0.0","db":"up","openfga":"up","storeReady":true}`
	if w.Body.String() != want {
		t.Fatalf("body = %s\nwant  = %s", w.Body.String(), want)
	}
}

func TestHealth_degraded(t *testing.T) {
	tests := []struct {
		name       string
		db, fga    bool
		ready      bool
		wantDB     string
		wantFGA    string
		wantReady  bool
		wantStatus string
	}{
		{"db down", false, true, true, "down", "up", true, "degraded"},
		{"fga down", true, false, true, "up", "down", true, "degraded"},
		{"store not ready", true, true, false, "up", "up", false, "degraded"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := Health{
				Version:    "0.0.0",
				DBPing:     staticBool(tc.db),
				FGAPing:    staticBool(tc.fga),
				StoreReady: func() bool { return tc.ready },
			}
			w := httptest.NewRecorder()
			h.Handler()(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if w.Code != 503 {
				t.Fatalf("code = %d, want 503", w.Code)
			}
		})
	}
}

func TestNewRouter_healthz(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := NewRouter(logger, Health{
		Version:    "0.0.0",
		DBPing:     staticBool(true),
		FGAPing:    staticBool(true),
		StoreReady: func() bool { return true },
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"status":"ok","version":"0.0.0","db":"up","openfga":"up","storeReady":true}` {
		t.Fatalf("body = %s", body)
	}

	// 알 수 없는 경로는 chi 기본 404.
	resp2, err := http.Get(srv.URL + "/nope")
	if err != nil {
		t.Fatalf("GET /nope: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != 404 {
		t.Errorf("unknown path status = %d, want 404", resp2.StatusCode)
	}
}
