package httpx

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestRequestLogger(t *testing.T) {
	t.Run("logs explicit status", func(t *testing.T) {
		logger, buf := bufLogger()
		h := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", nil))
		out := buf.String()
		if !strings.Contains(out, "status=201") || !strings.Contains(out, "method=POST") {
			t.Fatalf("log missing fields: %s", out)
		}
	})
	t.Run("defaults to 200 on write without WriteHeader", func(t *testing.T) {
		logger, buf := bufLogger()
		h := RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("hi"))
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if !strings.Contains(buf.String(), "status=200") {
			t.Fatalf("expected status 200, got %s", buf.String())
		}
		if rec.Body.String() != "hi" {
			t.Errorf("body = %q", rec.Body.String())
		}
	})
	t.Run("no write leaves default 200", func(t *testing.T) {
		logger, buf := bufLogger()
		h := RequestLogger(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if !strings.Contains(buf.String(), "status=200") {
			t.Fatalf("expected default 200, got %s", buf.String())
		}
	})
}

func TestRecoverer(t *testing.T) {
	logger, buf := bufLogger()
	h := Recoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if w.Code != 500 || w.Body.String() != "Internal Server Error" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(buf.String(), "panic recovered") {
		t.Errorf("panic not logged: %s", buf.String())
	}
}

func TestRecoverer_noPanicPassthrough(t *testing.T) {
	logger, _ := bufLogger()
	h := Recoverer(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, 200, map[string]string{"ok": "yes"})
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestBodyLimit(t *testing.T) {
	const limit = 16

	t.Run("content-length over limit -> 413", func(t *testing.T) {
		called := false
		h := BodyLimit(limit)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", 100)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 413 || w.Body.String() != `{"error":"payload too large"}` {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		if called {
			t.Error("handler must not run when body too large")
		}
	})

	t.Run("within limit passes through and body readable", func(t *testing.T) {
		var got string
		h := BodyLimit(limit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			got = string(b)
		}))
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("small"))
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got != "small" {
			t.Fatalf("body = %q", got)
		}
	})

	t.Run("chunked overflow trips MaxBytesReader in handler", func(t *testing.T) {
		var readErr error
		h := BodyLimit(limit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
		}))
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("b", 100)))
		req.ContentLength = -1 // 길이 불명(chunked) 취급 → Content-Length 빠른 경로 우회.
		h.ServeHTTP(httptest.NewRecorder(), req)
		if readErr == nil {
			t.Fatal("expected MaxBytesReader to error on oversized chunked body")
		}
	})
}
