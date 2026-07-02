package db

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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requireIntegration은 DATABASE_URL을 요구한다. 미설정이면 skip하되,
// LAZYFGA_TEST_INTEGRATION=1이면 skip 대신 실패시킨다(CI 게이트 모드).
func requireIntegration(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		if os.Getenv("LAZYFGA_TEST_INTEGRATION") == "1" {
			t.Fatal("LAZYFGA_TEST_INTEGRATION=1 but DATABASE_URL is unset")
		}
		t.Skip("DATABASE_URL unset; skipping DB integration test")
	}
	return raw
}

// urlWithDBName은 URL의 데이터베이스 이름만 바꾼다(쿼리 파라미터 보존).
func urlWithDBName(raw, dbName string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func randSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// newScratchDB는 유지보수 DB(postgres)에 접속해 임시 DB를 만들고 그 풀을 반환한다.
// CRITICAL: 사용자 개발 데이터가 있는 lazyfga DB에는 절대 쓰지 않는다 — 항상 별도 임시 DB.
func newScratchDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := requireIntegration(t)

	scratchName := "lazyfga_test_" + randSuffix(t)
	if scratchName == "lazyfga" || !strings.HasPrefix(scratchName, "lazyfga_test_") {
		t.Fatalf("unsafe scratch db name: %q", scratchName)
	}
	adminURL, err := urlWithDBName(raw, "postgres")
	if err != nil {
		t.Fatalf("admin url: %v", err)
	}
	scratchURL, err := urlWithDBName(raw, scratchName)
	if err != nil {
		t.Fatalf("scratch url: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin db: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, scratchName)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch db: %v", err)
	}
	_ = admin.Close(ctx)

	pool, err := Connect(context.Background(), scratchURL)
	if err != nil {
		t.Fatalf("connect scratch pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dcancel()
		a, err := pgx.Connect(dctx, adminURL)
		if err != nil {
			t.Logf("cleanup connect admin: %v", err)
			return
		}
		defer func() { _ = a.Close(dctx) }()
		_, _ = a.Exec(dctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, scratchName)
		if _, err := a.Exec(dctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, scratchName)); err != nil {
			t.Logf("cleanup drop db: %v", err)
		}
	})
	return pool
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`, name).Scan(&exists)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return exists
}

func bookkeepingCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM drizzle.__drizzle_migrations`).Scan(&n); err != nil {
		t.Fatalf("bookkeepingCount: %v", err)
	}
	return n
}

var allTables = []string{
	"instance_config", "model_version", "policy", "service_token",
	"idp_connection", "idp_mapping_rule", "audit_log",
}

func TestIntegration_MigrateFresh(t *testing.T) {
	pool := newScratchDB(t)
	m := NewMigrator()
	if err := m.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, tbl := range allTables {
		if !tableExists(t, pool, tbl) {
			t.Errorf("table %s not created", tbl)
		}
	}
	if got := bookkeepingCount(t, pool); got != 7 {
		t.Errorf("bookkeeping rows = %d, want 7", got)
	}
	if !Ping(context.Background(), pool) {
		t.Error("Ping should be true against a live scratch DB")
	}
}

func TestIntegration_MigrateIdempotent(t *testing.T) {
	pool := newScratchDB(t)
	m := NewMigrator()
	if err := m.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	// 재실행은 완전한 no-op이어야 한다(부트키핑 행 불변, 오류 없음).
	if err := m.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if got := bookkeepingCount(t, pool); got != 7 {
		t.Errorf("bookkeeping rows after re-run = %d, want 7", got)
	}
}

func TestIntegration_AdoptDrizzleBookkeeping(t *testing.T) {
	pool := newScratchDB(t)
	ctx := context.Background()
	// Drizzle이 남긴 부트키핑을 재현: 0000~0006 when 값을 미리 넣되 실제 테이블은 만들지 않는다.
	if _, err := pool.Exec(ctx, createSchemaSQL); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	migs, err := loadMigrations(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	for _, mig := range migs {
		if _, err := pool.Exec(ctx, insertSQL, mig.hash, mig.when); err != nil {
			t.Fatalf("seed bookkeeping: %v", err)
		}
	}
	// 채택: 이미 전부 적용된 것으로 인식 → 0 문장 실행 → 실제 테이블은 생기지 않는다.
	if err := NewMigrator().Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate (adopt): %v", err)
	}
	if tableExists(t, pool, "model_version") {
		t.Error("adoption must be a no-op: no real tables should be created")
	}
	if got := bookkeepingCount(t, pool); got != 7 {
		t.Errorf("bookkeeping rows = %d, want 7 (unchanged)", got)
	}
}

func TestIntegration_MigratePartialThenApply(t *testing.T) {
	pool := newScratchDB(t)
	ctx := context.Background()
	migs, err := loadMigrations(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	// 0000~0004까지의 스키마 상태를 실제로 만든 뒤 부트키핑도 그만큼만 심는다.
	if _, err := pool.Exec(ctx, createSchemaSQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		t.Fatalf("table: %v", err)
	}
	for _, mig := range migs[:5] {
		for _, stmt := range mig.statements {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := pool.Exec(ctx, stmt, pgx.QueryExecModeSimpleProtocol); err != nil {
				t.Fatalf("apply %s: %v", mig.tag, err)
			}
		}
		if _, err := pool.Exec(ctx, insertSQL, mig.hash, mig.when); err != nil {
			t.Fatalf("seed bookkeeping %s: %v", mig.tag, err)
		}
	}
	// 이제 0005/0006만 적용되어야 한다.
	if err := NewMigrator().Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate (partial): %v", err)
	}
	if got := bookkeepingCount(t, pool); got != 7 {
		t.Errorf("bookkeeping rows = %d, want 7", got)
	}
	// 0006 효과 확인: idp_connection.preset, idp_mapping_rule.fan_out 컬럼 존재.
	if !columnExists(t, pool, "idp_connection", "preset") {
		t.Error("0006 not applied: idp_connection.preset missing")
	}
	if !columnExists(t, pool, "idp_mapping_rule", "fan_out") {
		t.Error("0006 not applied: idp_mapping_rule.fan_out missing")
	}
}

func columnExists(t *testing.T, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name=$2)`,
		table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("columnExists: %v", err)
	}
	return exists
}
