package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newMigratedScratchDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		if os.Getenv("LAZYFGA_TEST_INTEGRATION") == "1" {
			t.Fatal("LAZYFGA_TEST_INTEGRATION=1 but DATABASE_URL is unset")
		}
		t.Skip("DATABASE_URL unset; skipping DB integration test")
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	scratchName := "lazyfga_test_" + hex.EncodeToString(b)
	if !strings.HasPrefix(scratchName, "lazyfga_test_") {
		t.Fatalf("unsafe scratch name %q", scratchName)
	}
	withDB := func(name string) string {
		u, _ := url.Parse(raw)
		u.Path = "/" + name
		return u.String()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, withDB("postgres"))
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, scratchName)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch: %v", err)
	}
	_ = admin.Close(ctx)

	pool, err := db.Connect(context.Background(), withDB(scratchName))
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	if err := db.NewMigrator().Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate scratch: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dcancel()
		a, err := pgx.Connect(dctx, withDB("postgres"))
		if err != nil {
			return
		}
		defer func() { _ = a.Close(dctx) }()
		_, _ = a.Exec(dctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, scratchName)
		_, _ = a.Exec(dctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, scratchName))
	})
	return pool
}

func TestIntegration_RecordWritesJSONB(t *testing.T) {
	pool := newMigratedScratchDB(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := syncRecorder(pool, logger) // 동기 디스패치로 결정적 관찰.

	r.Record("model.publish", map[string]any{"versionId": "v1", "n": 3}, "admin")

	var action, actor string
	var data map[string]any
	err := pool.QueryRow(context.Background(),
		`SELECT action, actor, data FROM audit_log ORDER BY occurred_at DESC LIMIT 1`).
		Scan(&action, &actor, &data)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if action != "model.publish" || actor != "admin" {
		t.Errorf("action=%q actor=%q", action, actor)
	}
	if data["versionId"] != "v1" {
		t.Errorf("data.versionId = %v", data["versionId"])
	}

	// 기본값 경로: data nil → {}, actor "" → system.
	r.Record("noop", nil, "")
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE actor='system' AND data='{}'::jsonb`).Scan(&count); err != nil {
		t.Fatalf("count defaults: %v", err)
	}
	if count != 1 {
		t.Errorf("default-actor rows = %d, want 1", count)
	}
}
