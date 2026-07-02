package auth

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

func TestSha256Hex(t *testing.T) {
	// 알려진 벡터: sha256("")의 hex.
	if got := Sha256Hex(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("Sha256Hex(empty) = %s", got)
	}
	if len(Sha256Hex("abc")) != 64 {
		t.Fatal("hash must be 64 hex chars")
	}
}

func TestGenerateToken(t *testing.T) {
	plain, hash, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if len(plain) <= 20 {
		t.Errorf("plain too short: %q", plain)
	}
	if hash != Sha256Hex(plain) {
		t.Errorf("hash does not match sha256(plain)")
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64", len(hash))
	}
	// 유일성.
	p2, _, _ := GenerateToken()
	if plain == p2 {
		t.Error("tokens must be unique")
	}
}

// ---- 통합 테스트(scratch DB) ----

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

func TestRepo_CRUD(t *testing.T) {
	pool := newMigratedScratchDB(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	created, err := repo.Create(ctx, "ci-token", Sha256Hex("secret-plain"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" || created.Name != "ci-token" {
		t.Fatalf("unexpected created row: %+v", created)
	}
	if created.CreatedAt.IsZero() {
		t.Error("createdAt should be set")
	}
	if created.LastUsedAt != nil || created.RevokedAt != nil {
		t.Error("new token should have nil last_used_at/revoked_at")
	}

	// FindActiveByHash: 존재.
	found, err := repo.FindActiveByHash(ctx, Sha256Hex("secret-plain"))
	if err != nil {
		t.Fatalf("FindActiveByHash: %v", err)
	}
	if found == nil || found.ID != created.ID {
		t.Fatalf("expected to find created token, got %+v", found)
	}
	// FindActiveByHash: 없음 → nil,nil.
	missing, err := repo.FindActiveByHash(ctx, Sha256Hex("nope"))
	if err != nil {
		t.Fatalf("FindActiveByHash(miss): %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for unknown hash, got %+v", missing)
	}

	// TouchLastUsed.
	if err := repo.TouchLastUsed(ctx, created.ID); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}
	touched, _ := repo.FindActiveByHash(ctx, Sha256Hex("secret-plain"))
	if touched.LastUsedAt == nil {
		t.Error("last_used_at should be set after touch")
	}

	// List: 정렬(추가 토큰 후 desc).
	time.Sleep(2 * time.Millisecond)
	if _, err := repo.Create(ctx, "second", Sha256Hex("second-plain")); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	if list[0].Name != "second" {
		t.Errorf("List not ordered desc: first = %q", list[0].Name)
	}

	// Revoke: 성공 → 이후 활성 조회 실패, 재폐기 false.
	ok, err := repo.Revoke(ctx, created.ID)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if !ok {
		t.Fatal("Revoke should return true for active token")
	}
	afterRevoke, _ := repo.FindActiveByHash(ctx, Sha256Hex("secret-plain"))
	if afterRevoke != nil {
		t.Error("revoked token must not be active")
	}
	if again, _ := repo.Revoke(ctx, created.ID); again {
		t.Error("re-revoking should return false")
	}

	// Revoke unknown uuid → false.
	if got, _ := repo.Revoke(ctx, "00000000-0000-0000-0000-000000000000"); got {
		t.Error("revoking unknown id should return false")
	}
}
