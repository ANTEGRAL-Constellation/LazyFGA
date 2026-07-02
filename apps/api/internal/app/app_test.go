package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/config"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
	"github.com/jackc/pgx/v5/pgxpool"
	fga "github.com/openfga/go-sdk"
)

// newFakeFGAServer는 임의 요청에 200을 반환하는 최소 OpenFGA 스텁 URL을 반환한다.
// (NewGateway는 유효한 URL만 필요로 하며, 사전 취소된 ctx에서는 서버 호출이 없다.)
func newFakeFGAServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"stores":[],"continuation_token":""}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func noopSleep(context.Context, time.Duration) {}

// fakeGateway는 openfga.Gateway를 만족하는 테스트용 구현이다.
type fakeGateway struct {
	bootstrapErr   error
	bootstrapCalls atomic.Int32
	ping           bool
}

func (f *fakeGateway) Bootstrap(context.Context, openfga.BootstrapOptions) (string, error) {
	f.bootstrapCalls.Add(1)
	if f.bootstrapErr != nil {
		return "", f.bootstrapErr
	}
	return "store-x", nil
}
func (f *fakeGateway) StoreID() (string, error)  { return "store-x", nil }
func (f *fakeGateway) Ping(context.Context) bool { return f.ping }
func (f *fakeGateway) Check(context.Context, openfga.CheckInput, ...openfga.CheckOption) (bool, error) {
	return false, nil
}
func (f *fakeGateway) Read(context.Context, openfga.ReadInput) ([]openfga.ReadTuple, error) {
	return nil, nil
}
func (f *fakeGateway) Write(context.Context, openfga.WriteInput, ...openfga.WriteOption) error {
	return nil
}
func (f *fakeGateway) WriteAuthorizationModel(context.Context, fga.WriteAuthorizationModelRequest) (string, error) {
	return "", nil
}

type fakeMigrator struct {
	err   error
	calls int
}

func (f *fakeMigrator) Migrate(context.Context, *pgxpool.Pool) error {
	f.calls++
	return f.err
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"syscall econnrefused", syscall.ECONNREFUSED, true},
		{"net op error", &net.OpError{Op: "dial", Err: syscall.ECONNRESET}, true},
		{"dns error", &net.DNSError{Err: "no such host"}, true},
		{"msg econnrefused", errors.New("dial ECONNREFUSED"), true},
		{"msg connect", errors.New("could not connect to server"), true},
		{"msg fetch failed", errors.New("fetch failed"), true},
		{"msg network", errors.New("Network unreachable"), true},
		{"msg timeout", errors.New("i/o Timeout"), true},
		{"msg getaddrinfo", errors.New("getaddrinfo failed"), true},
		{"non-transient", errors.New("permission denied"), false},
	}
	for _, tc := range tests {
		if got := isTransient(tc.err); got != tc.want {
			t.Errorf("%s: isTransient = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	cases := map[int]time.Duration{
		1: 1000 * time.Millisecond,
		2: 2000 * time.Millisecond,
		5: 5000 * time.Millisecond,
		6: 5000 * time.Millisecond,
		9: 5000 * time.Millisecond,
	}
	for i, want := range cases {
		if got := backoff(i); got != want {
			t.Errorf("backoff(%d) = %v, want %v", i, got, want)
		}
	}
}

func TestSleepCtx(t *testing.T) {
	t.Run("returns on ctx cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		sleepCtx(ctx, time.Hour)
		if time.Since(start) > time.Second {
			t.Fatal("should return immediately on canceled ctx")
		}
	})
	t.Run("returns on timer", func(t *testing.T) {
		start := time.Now()
		sleepCtx(context.Background(), 5*time.Millisecond)
		if time.Since(start) < 3*time.Millisecond {
			t.Fatal("should wait for timer")
		}
	})
}

func newApp(gw *fakeGateway, mig Migrator) *App {
	return &App{
		Config:   config.Config{Port: 0},
		Logger:   discardLogger(),
		Gateway:  gw,
		Migrator: mig,
		Attempts: 3,
		Sleep:    noopSleep,
	}
}

func TestWithRetry(t *testing.T) {
	t.Run("success first try", func(t *testing.T) {
		a := newApp(&fakeGateway{}, &fakeMigrator{})
		calls := 0
		err := a.withRetry(context.Background(), func() error { calls++; return nil }, "x")
		if err != nil || calls != 1 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})
	t.Run("transient then success", func(t *testing.T) {
		a := newApp(&fakeGateway{}, &fakeMigrator{})
		calls := 0
		err := a.withRetry(context.Background(), func() error {
			calls++
			if calls < 3 {
				return syscall.ECONNREFUSED
			}
			return nil
		}, "x")
		if err != nil || calls != 3 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})
	t.Run("fatal returns immediately", func(t *testing.T) {
		a := newApp(&fakeGateway{}, &fakeMigrator{})
		fatal := errors.New("permission denied")
		calls := 0
		err := a.withRetry(context.Background(), func() error { calls++; return fatal }, "x")
		if !errors.Is(err, fatal) || calls != 1 {
			t.Fatalf("err=%v calls=%d, want fatal on first call", err, calls)
		}
	})
	t.Run("transient exhaustion returns last error", func(t *testing.T) {
		a := newApp(&fakeGateway{}, &fakeMigrator{})
		a.Attempts = 3
		calls := 0
		err := a.withRetry(context.Background(), func() error { calls++; return syscall.ECONNREFUSED }, "x")
		if err == nil || calls != 3 {
			t.Fatalf("err=%v calls=%d, want 3 attempts", err, calls)
		}
	})
	t.Run("ctx canceled bails", func(t *testing.T) {
		a := newApp(&fakeGateway{}, &fakeMigrator{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := a.withRetry(ctx, func() error { return nil }, "x")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestBootstrap(t *testing.T) {
	t.Run("happy sets storeReady", func(t *testing.T) {
		gw := &fakeGateway{}
		a := newApp(gw, &fakeMigrator{})
		if err := a.bootstrap(context.Background()); err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		if !a.storeReady.Load() {
			t.Fatal("storeReady should be true after success")
		}
	})
	t.Run("migrate transient -> degraded", func(t *testing.T) {
		a := newApp(&fakeGateway{}, &fakeMigrator{err: syscall.ECONNREFUSED})
		if err := a.bootstrap(context.Background()); err != nil {
			t.Fatalf("degraded should return nil, got %v", err)
		}
		if a.storeReady.Load() {
			t.Fatal("storeReady must stay false when degraded")
		}
	})
	t.Run("migrate fatal -> error", func(t *testing.T) {
		fatal := errors.New("bad schema")
		a := newApp(&fakeGateway{}, &fakeMigrator{err: fatal})
		if err := a.bootstrap(context.Background()); !errors.Is(err, fatal) {
			t.Fatalf("err = %v, want fatal", err)
		}
	})
	t.Run("gateway transient -> degraded", func(t *testing.T) {
		a := newApp(&fakeGateway{bootstrapErr: syscall.ECONNREFUSED}, &fakeMigrator{})
		if err := a.bootstrap(context.Background()); err != nil {
			t.Fatalf("degraded should return nil, got %v", err)
		}
		if a.storeReady.Load() {
			t.Fatal("storeReady must stay false")
		}
	})
	t.Run("gateway fatal -> error", func(t *testing.T) {
		fatal := errors.New("invalid config")
		a := newApp(&fakeGateway{bootstrapErr: fatal}, &fakeMigrator{})
		if err := a.bootstrap(context.Background()); !errors.Is(err, fatal) {
			t.Fatalf("err = %v, want fatal", err)
		}
	})
}

func TestApplyDefaults(t *testing.T) {
	a := &App{}
	a.applyDefaults()
	if a.Logger == nil {
		t.Error("Logger should default")
	}
	if a.Attempts != defaultAttempts {
		t.Errorf("Attempts = %d, want %d", a.Attempts, defaultAttempts)
	}
	if a.Sleep == nil {
		t.Error("Sleep should default")
	}
	if a.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %v", a.ShutdownTimeout)
	}
}

// ---- Serve ----

func waitHealthz(t *testing.T, addr string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			return resp
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not come up")
	return nil
}

func TestServe_degradedThenShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{
		Config:   config.Config{Port: 0},
		Logger:   discardLogger(),
		Gateway:  &fakeGateway{bootstrapErr: syscall.ECONNREFUSED, ping: false},
		Migrator: &fakeMigrator{},
		Attempts: 2,
		Sleep:    noopSleep,
		Listener: ln,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Serve(ctx) }()

	resp := waitHealthz(t, ln.Addr().String())
	if resp.StatusCode != 503 {
		t.Errorf("degraded healthz = %d, want 503", resp.StatusCode)
	}
	_ = resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil on graceful shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not shut down")
	}
}

func TestServe_fatalReturnsError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	fatal := errors.New("bad openfga config")
	a := &App{
		Config:   config.Config{Port: 0},
		Logger:   discardLogger(),
		Gateway:  &fakeGateway{bootstrapErr: fatal},
		Migrator: &fakeMigrator{},
		Attempts: 1,
		Sleep:    noopSleep,
		Listener: ln,
	}
	done := make(chan error, 1)
	go func() { done <- a.Serve(context.Background()) }()
	select {
	case err := <-done:
		if !errors.Is(err, fatal) {
			t.Fatalf("Serve returned %v, want fatal", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return on fatal bootstrap error")
	}
}

func TestServe_listenError(t *testing.T) {
	a := &App{
		Config:   config.Config{Port: -1}, // 잘못된 포트 → net.Listen 실패.
		Logger:   discardLogger(),
		Gateway:  &fakeGateway{},
		Migrator: &fakeMigrator{},
		Sleep:    noopSleep,
	}
	if err := a.Serve(context.Background()); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestServe_serverError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_ = ln.Close() // 닫힌 리스너 → srv.Serve 즉시 오류.
	a := &App{
		Config:   config.Config{Port: 0},
		Logger:   discardLogger(),
		Gateway:  &fakeGateway{ping: true},
		Migrator: &fakeMigrator{},
		Attempts: 1,
		Sleep:    noopSleep,
		Listener: ln,
	}
	if err := a.Serve(context.Background()); err == nil {
		t.Fatal("expected serve error from closed listener")
	}
}

func TestRun_gatewayError(t *testing.T) {
	t.Setenv("OPENFGA_API_URL", "://bad-url")
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err := Run(context.Background()); err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("expected gateway error, got %v", err)
	}
}

func TestRun_dbConnectError(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u@host:notaport/db")
	if err := Run(context.Background()); err == nil || !strings.Contains(err.Error(), "connect db") {
		t.Fatalf("expected db connect error, got %v", err)
	}
}

func TestRun_successGracefulShutdown(t *testing.T) {
	fake := newFakeFGAServer(t)
	t.Setenv("PORT", "0")
	t.Setenv("OPENFGA_API_URL", fake)
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 사전 취소 → Serve 즉시 graceful shutdown.
	if err := Run(ctx); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
}
