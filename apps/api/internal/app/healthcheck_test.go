package app

import (
	"context"
	"net"
	"net/http"
	"testing"
)

type fakeDoer struct {
	resp *http.Response
	err  error
}

func (f fakeDoer) Do(*http.Request) (*http.Response, error) { return f.resp, f.err }

func TestHealthcheckStatus(t *testing.T) {
	t.Run("200 -> 0", func(t *testing.T) {
		d := fakeDoer{resp: &http.Response{StatusCode: 200, Body: http.NoBody}}
		if got := healthcheckStatus(context.Background(), d, "http://x/healthz"); got != 0 {
			t.Fatalf("got %d, want 0", got)
		}
	})
	t.Run("503 -> 1", func(t *testing.T) {
		d := fakeDoer{resp: &http.Response{StatusCode: 503, Body: http.NoBody}}
		if got := healthcheckStatus(context.Background(), d, "http://x/healthz"); got != 1 {
			t.Fatalf("got %d, want 1", got)
		}
	})
	t.Run("client error -> 1", func(t *testing.T) {
		d := fakeDoer{err: context.DeadlineExceeded}
		if got := healthcheckStatus(context.Background(), d, "http://x/healthz"); got != 1 {
			t.Fatalf("got %d, want 1", got)
		}
	})
	t.Run("bad url -> 1", func(t *testing.T) {
		if got := healthcheckStatus(context.Background(), fakeDoer{}, "://bad url"); got != 1 {
			t.Fatalf("got %d, want 1", got)
		}
	})
}

func TestHealthcheck_liveServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	t.Setenv("PORT", port)
	if got := Healthcheck(context.Background()); got != 0 {
		t.Fatalf("Healthcheck = %d, want 0 against live 200 server", got)
	}
}

func TestHealthcheck_noServer(t *testing.T) {
	// 아무도 리스닝하지 않는 포트 → 연결 실패 → 1.
	t.Setenv("PORT", "1") // 특권 포트, 대개 리스닝 없음.
	if got := Healthcheck(context.Background()); got != 1 {
		t.Fatalf("Healthcheck = %d, want 1 when no server", got)
	}
}
