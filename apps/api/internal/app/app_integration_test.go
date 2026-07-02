package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

func TestIntegration_StoreIDPersistence(t *testing.T) {
	pool := newMigratedScratchDB(t)
	a := &App{Pool: pool}
	ctx := context.Background()

	// 초기: 저장된 store id 없음 → "".
	got, err := a.loadStoredStoreID(ctx)
	if err != nil {
		t.Fatalf("loadStoredStoreID: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	// upsert(insert).
	if err := a.persistStoreID(ctx, "store-1"); err != nil {
		t.Fatalf("persistStoreID: %v", err)
	}
	got, _ = a.loadStoredStoreID(ctx)
	if got != "store-1" {
		t.Fatalf("after insert = %q, want store-1", got)
	}

	// upsert(update, 싱글턴 유지).
	if err := a.persistStoreID(ctx, "store-2"); err != nil {
		t.Fatalf("persistStoreID update: %v", err)
	}
	got, _ = a.loadStoredStoreID(ctx)
	if got != "store-2" {
		t.Fatalf("after update = %q, want store-2", got)
	}

	// 싱글턴 행 1개만 존재.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM instance_config`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("instance_config rows = %d, want 1 (singleton)", count)
	}
}
