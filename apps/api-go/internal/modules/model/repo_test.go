package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// ---- 통합 테스트(scratch DB, auth/repo_test.go 패턴 재사용) ----

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

func urlWithDBName(raw, dbName string) string {
	u, _ := url.Parse(raw)
	u.Path = "/" + dbName
	return u.String()
}

func newMigratedScratchDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := requireIntegration(t)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	scratchName := "lazyfga_test_" + hex.EncodeToString(b)
	if !strings.HasPrefix(scratchName, "lazyfga_test_") {
		t.Fatalf("unsafe scratch name %q", scratchName)
	}
	adminURL := urlWithDBName(raw, "postgres")
	scratchURL := urlWithDBName(raw, scratchName)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, scratchName)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch: %v", err)
	}
	_ = admin.Close(ctx)

	pool, err := db.Connect(context.Background(), scratchURL)
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
		a, err := pgx.Connect(dctx, adminURL)
		if err != nil {
			return
		}
		defer func() { _ = a.Close(dctx) }()
		_, _ = a.Exec(dctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, scratchName)
		_, _ = a.Exec(dctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, scratchName))
	})
	return pool
}

func seedSingleton(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO instance_config (id, openfga_store_id) VALUES ('singleton', 'store-test')`); err != nil {
		t.Fatalf("seed singleton: %v", err)
	}
}

const testIR = `{"schemaVersion":"1.1","groups":[],"resources":[{"name":"doc","parents":[],"roles":[{"name":"owner","assignableBy":[{"kind":"user"}]}],"permissions":[{"name":"read","grantedByRoles":["owner"],"inheritFromParents":[]}]}]}`

func TestRepo_Integration(t *testing.T) {
	pool := newMigratedScratchDB(t)
	seedSingleton(t, pool)
	repo := NewRepo(pool)
	ctx := context.Background()

	// 초기: 포인터 미설정 → CurrentVersion nil.
	if cur, err := repo.CurrentVersion(ctx); err != nil || cur != nil {
		t.Fatalf("CurrentVersion initial = %v, %v", cur, err)
	}

	note := "v1 note"
	pv, err := repo.InsertVersion(ctx, InsertParams{
		AuthorizationModelID: "authz-1",
		IRJSON:               json.RawMessage(testIR),
		DSL:                  "model\n  schema 1.1",
		Note:                 &note,
		CreatedBy:            "admin",
	})
	if err != nil {
		t.Fatalf("InsertVersion: %v", err)
	}
	if pv.ID == "" || pv.AuthorizationModelID != "authz-1" || pv.CreatedAt.IsZero() {
		t.Fatalf("published = %+v", pv)
	}

	// CurrentVersion는 이제 방금 발행본을 가리킨다.
	cur, err := repo.CurrentVersion(ctx)
	if err != nil || cur == nil {
		t.Fatalf("CurrentVersion after insert = %v, %v", cur, err)
	}
	if cur.ID != pv.ID || cur.AuthorizationModelID != "authz-1" || cur.Note == nil || *cur.Note != note {
		t.Fatalf("current = %+v", cur)
	}
	// IRJSON은 유효 JSON이며 타입 IR로 역직렬화된다(JSONB 정규화 후에도).
	ir, err := cur.IR()
	if err != nil || len(ir.Resources) != 1 || ir.Resources[0].Name != "doc" {
		t.Fatalf("IR() = %+v, %v", ir, err)
	}

	// 두 번째 발행: created_at desc 정렬 확인.
	time.Sleep(2 * time.Millisecond)
	pv2, err := repo.InsertVersion(ctx, InsertParams{AuthorizationModelID: "authz-2", IRJSON: json.RawMessage(testIR), DSL: "d2", Note: nil, CreatedBy: "token:abc"})
	if err != nil {
		t.Fatalf("InsertVersion 2: %v", err)
	}
	list, err := repo.ListVersions(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListVersions = %d, %v", len(list), err)
	}
	if list[0].ID != pv2.ID {
		t.Errorf("ListVersions not desc: first = %s", list[0].ID)
	}
	if list[1].Note == nil || *list[1].Note != note {
		t.Errorf("note not preserved on first version")
	}
	if list[0].Note != nil {
		t.Errorf("second version note should be null")
	}

	// GetVersion: 존재/부재/malformed.
	got, err := repo.GetVersion(ctx, pv.ID)
	if err != nil || got == nil || got.ID != pv.ID {
		t.Fatalf("GetVersion(existing) = %v, %v", got, err)
	}
	missing, err := repo.GetVersion(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil || missing != nil {
		t.Fatalf("GetVersion(absent uuid) = %v, %v", missing, err)
	}
	malformed, err := repo.GetVersion(ctx, "not-a-uuid")
	if err != nil || malformed != nil {
		t.Fatalf("GetVersion(malformed) = %v, %v", malformed, err)
	}
}
