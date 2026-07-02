// Package app는 부트스트랩 오케스트레이션을 담당한다(TS index.ts 포팅).
// config 로드 → DB 연결 → gateway 구성 → 즉시 리스닝 → 백그라운드 부트스트랩
// (migrate → FGA bootstrap, 일시적 오류 backoff 재시도) → degraded/fatal 처리 → graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/config"
	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Version은 lazyFGA control-plane API 버전이다.
const Version = "0.0.0"

const (
	defaultAttempts        = 8
	defaultShutdownTimeout = 10 * time.Second
)

// Migrator는 부팅 마이그레이션 실행자다(db.Migrator가 만족).
type Migrator interface {
	Migrate(ctx context.Context, pool *pgxpool.Pool) error
}

// App은 서버 실행에 필요한 의존성 묶음이다. 필드는 테스트에서 fake로 주입 가능하다.
type App struct {
	Config   config.Config
	Logger   *slog.Logger
	Pool     *pgxpool.Pool
	Gateway  openfga.Gateway
	Migrator Migrator

	// 조정 가능한 노브(0/nil이면 기본값 적용).
	Attempts        int
	Sleep           func(ctx context.Context, d time.Duration)
	Listener        net.Listener
	ShutdownTimeout time.Duration

	storeReady atomic.Bool
}

// Run은 실운영 경로다: 설정 로드 → 연결 → gateway → Serve.
func Run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("app: load config: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("app: connect db: %w", err)
	}
	defer pool.Close()

	gw, err := openfga.NewGateway(cfg.OpenFGAAPIURL, logger)
	if err != nil {
		return fmt.Errorf("app: create openfga gateway: %w", err)
	}

	a := &App{
		Config:   cfg,
		Logger:   logger,
		Pool:     pool,
		Gateway:  gw,
		Migrator: db.NewMigrator(),
	}
	return a.Serve(ctx)
}

func (a *App) applyDefaults() {
	if a.Logger == nil {
		a.Logger = slog.Default()
	}
	if a.Attempts <= 0 {
		a.Attempts = defaultAttempts
	}
	if a.Sleep == nil {
		a.Sleep = sleepCtx
	}
	if a.ShutdownTimeout <= 0 {
		a.ShutdownTimeout = defaultShutdownTimeout
	}
}

// Serve는 즉시 리스닝하고 백그라운드 부트스트랩을 돌린 뒤, ctx 취소/시그널 시 graceful shutdown한다.
func (a *App) Serve(ctx context.Context) error {
	a.applyDefaults()

	if a.Config.AdminTokenInsecure() {
		a.Logger.Warn("ADMIN_TOKEN is empty or the known placeholder — the control plane (model/policy/token) is unreachable or insecure. Set ADMIN_TOKEN.")
	}

	ln := a.Listener
	if ln == nil {
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", a.Config.Port))
		if err != nil {
			return fmt.Errorf("app: listen: %w", err)
		}
		ln = l
	}
	a.Logger.Info("listening", "addr", ln.Addr().String())

	srv := &http.Server{Handler: a.handler()}

	// 백그라운드 부트스트랩: 비일시적 fatal만 fatalCh로 보고한다(ctx 취소는 fatal 아님).
	fatalCh := make(chan error, 1)
	go func() {
		if err := a.bootstrap(ctx); err != nil && ctx.Err() == nil {
			fatalCh <- err
		}
	}()

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- srv.Serve(ln) }()

	var retErr error
	select {
	case <-ctx.Done():
		a.Logger.Info("shutdown signal received")
	case err := <-fatalCh:
		a.Logger.Error("fatal bootstrap error; exiting", "err", err)
		retErr = err
	case err := <-serveErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("app: serve: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		a.Logger.Error("graceful shutdown error", "err", err)
	}
	return retErr
}

// handler는 공통 미들웨어 + /healthz를 붙인 라우터를 만든다.
func (a *App) handler() http.Handler {
	health := httpx.Health{
		Version:    Version,
		DBPing:     func(r *http.Request) bool { return db.Ping(r.Context(), a.Pool) },
		FGAPing:    func(r *http.Request) bool { return a.Gateway.Ping(r.Context()) },
		StoreReady: a.storeReady.Load,
	}
	return httpx.NewRouter(a.Logger, health)
}

// bootstrap는 migrate → FGA bootstrap를 일시적 오류 재시도와 함께 수행한다.
// 성공 시 storeReady=true. 일시적 소진은 degraded(로그 후 nil), 비일시적은 fatal(오류 반환).
func (a *App) bootstrap(ctx context.Context) error {
	if err := a.withRetry(ctx, func() error { return a.Migrator.Migrate(ctx, a.Pool) }, "db migrate"); err != nil {
		return a.classifyBootstrapError(err)
	}

	var storeID string
	err := a.withRetry(ctx, func() error {
		id, e := a.Gateway.Bootstrap(ctx, openfga.BootstrapOptions{
			EnvStoreID:        a.Config.StoreID,
			LoadStoredStoreID: a.loadStoredStoreID,
			PersistStoreID:    a.persistStoreID,
		})
		storeID = id
		return e
	}, "openfga bootstrap")
	if err != nil {
		return a.classifyBootstrapError(err)
	}

	a.storeReady.Store(true)
	a.Logger.Info("lazyfga-api ready", "store", storeID, "port", a.Config.Port)
	return nil
}

// classifyBootstrapError는 일시적 오류를 degraded(nil)로, 비일시적을 fatal(원본)로 분류한다.
func (a *App) classifyBootstrapError(err error) error {
	if isTransient(err) {
		a.Logger.Error("dependencies unavailable after retries; serving in degraded mode (/healthz 503)", "err", err)
		return nil
	}
	return err
}

// withRetry는 일시적 오류에만 backoff 재시도한다. 비일시적 오류는 즉시 반환(재시도 무의미).
func (a *App) withRetry(ctx context.Context, fn func() error, label string) error {
	var lastErr error
	for i := 1; i <= a.Attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		if !isTransient(err) {
			return err // fatal: 재시도 무의미.
		}
		lastErr = err
		a.Logger.Warn("startup step failed (transient); retrying",
			"label", label, "attempt", i, "max", a.Attempts, "err", err)
		a.Sleep(ctx, backoff(i))
	}
	return lastErr
}

// backoff는 min(1000*i, 5000)ms를 반환한다(TS와 동일).
func backoff(i int) time.Duration {
	ms := 1000 * i
	if ms > 5000 {
		ms = 5000
	}
	return time.Duration(ms) * time.Millisecond
}

// sleepCtx는 ctx 취소 시 조기 반환하는 sleep이다.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// isTransient는 일시적(의존성 미기동) 오류인지 판별한다(TS index.ts isTransient 포팅).
// net.Error는 연결 거부/리셋/타임아웃/DNS(syscall.Errno·*net.OpError·*net.DNSError 포함)를 포괄한다.
// 메시지 폴백은 parity를 위해 유지한다.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "econnrefused") ||
		strings.Contains(m, "connect") ||
		strings.Contains(m, "fetch failed") ||
		strings.Contains(m, "network") ||
		strings.Contains(m, "timeout") ||
		strings.Contains(m, "getaddrinfo")
}

// loadStoredStoreID는 instance_config에서 저장된 store id를 로드한다(없으면 "").
func (a *App) loadStoredStoreID(ctx context.Context) (string, error) {
	var id string
	err := a.Pool.QueryRow(ctx, `SELECT openfga_store_id FROM instance_config LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// persistStoreID는 확정된 store id를 싱글턴 행에 upsert한다.
func (a *App) persistStoreID(ctx context.Context, storeID string) error {
	_, err := a.Pool.Exec(ctx,
		`INSERT INTO instance_config (id, openfga_store_id) VALUES ('singleton', $1)
		 ON CONFLICT (id) DO UPDATE SET openfga_store_id = EXCLUDED.openfga_store_id, updated_at = now()`,
		storeID)
	return err
}
