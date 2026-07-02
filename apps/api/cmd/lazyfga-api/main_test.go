package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func fakeFGA(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"stores":[],"continuation_token":""}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRun_healthcheckNoServer(t *testing.T) {
	t.Setenv("PORT", "1") // 리스닝 서버 없음 → 연결 실패 → 1.
	if got := run(context.Background(), []string{"lazyfga-api", "healthcheck"}); got != 1 {
		t.Fatalf("run(healthcheck) = %d, want 1", got)
	}
}

func TestRun_serveError(t *testing.T) {
	t.Setenv("OPENFGA_API_URL", "://bad-url")
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if got := run(context.Background(), []string{"lazyfga-api"}); got != 1 {
		t.Fatalf("run(serve, bad gateway) = %d, want 1", got)
	}
}

func TestRun_serveSuccessOnCanceledCtx(t *testing.T) {
	t.Setenv("PORT", "0")
	t.Setenv("OPENFGA_API_URL", fakeFGA(t))
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 사전 취소 → 즉시 graceful shutdown → 0.
	if got := run(ctx, []string{"lazyfga-api"}); got != 0 {
		t.Fatalf("run(serve, canceled) = %d, want 0", got)
	}
}

func TestMain_dispatch(t *testing.T) {
	// main()을 직접 호출하되 osExit를 가로채 실제 종료를 막는다(main 본문 커버).
	oldArgs, oldExit := os.Args, osExit
	defer func() { os.Args, osExit = oldArgs, oldExit }()

	t.Setenv("PORT", "1") // healthcheck 대상 서버 없음 → 1.
	var code int
	osExit = func(c int) { code = c }
	os.Args = []string{"lazyfga-api", "healthcheck"}

	main()

	if code != 1 {
		t.Fatalf("main dispatched exit code = %d, want 1", code)
	}
}
