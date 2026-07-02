package policy

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

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

func desc(s string) *string { return &s }

func TestRepo_Integration(t *testing.T) {
	pool := newMigratedScratchDB(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	// Insert + FindByID.
	created, err := repo.InsertPolicy(ctx, contract.Policy{ID: "doc-read", Permission: "read", ResourceType: "document", Description: desc("read docs")})
	if err != nil {
		t.Fatalf("InsertPolicy: %v", err)
	}
	if created.ID != "doc-read" || created.Description == nil || *created.Description != "read docs" {
		t.Fatalf("created = %+v", created)
	}
	found, err := repo.FindByID(ctx, "doc-read")
	if err != nil || found == nil || found.Permission != "read" {
		t.Fatalf("FindByID = %+v, %v", found, err)
	}
	// FindByID miss → nil.
	if miss, err := repo.FindByID(ctx, "nope"); err != nil || miss != nil {
		t.Fatalf("FindByID(miss) = %+v, %v", miss, err)
	}

	// FindByActionResource.
	ar, err := repo.FindByActionResource(ctx, "read", "document")
	if err != nil || ar == nil || ar.ID != "doc-read" {
		t.Fatalf("FindByActionResource = %+v, %v", ar, err)
	}
	if miss, err := repo.FindByActionResource(ctx, "read", "folder"); err != nil || miss != nil {
		t.Fatalf("FindByActionResource(miss) = %+v, %v", miss, err)
	}

	// UNIQUE(permission, resourceType) 위반 → 23505.
	_, err = repo.InsertPolicy(ctx, contract.Policy{ID: "other", Permission: "read", ResourceType: "document"})
	if err == nil || !isUniqueViolation(err) {
		t.Fatalf("expected 23505 on duplicate (perm,resource), got %v", err)
	}
	// PK(id) 위반 → 23505.
	_, err = repo.InsertPolicy(ctx, contract.Policy{ID: "doc-read", Permission: "write", ResourceType: "folder"})
	if err == nil || !isUniqueViolation(err) {
		t.Fatalf("expected 23505 on duplicate id, got %v", err)
	}

	// 정렬(created_at desc).
	time.Sleep(2 * time.Millisecond)
	if _, err := repo.InsertPolicy(ctx, contract.Policy{ID: "fold-write", Permission: "write", ResourceType: "folder"}); err != nil {
		t.Fatalf("InsertPolicy 2: %v", err)
	}
	list, err := repo.ListPolicies(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListPolicies = %d, %v", len(list), err)
	}
	if list[0].ID != "fold-write" {
		t.Errorf("ListPolicies not desc: first = %s", list[0].ID)
	}
	// description 없는 정책은 omitempty로 nil 유지.
	if list[0].Description != nil {
		t.Errorf("fold-write should have nil description")
	}

	// Update: description 갱신, condition_ref 불변.
	updated, err := repo.UpdatePolicy(ctx, "doc-read", UpdateParams{Permission: "read", ResourceType: "document", Description: desc("updated")})
	if err != nil || updated == nil || updated.Description == nil || *updated.Description != "updated" {
		t.Fatalf("UpdatePolicy = %+v, %v", updated, err)
	}
	// Update absent → nil.
	if u, err := repo.UpdatePolicy(ctx, "ghost", UpdateParams{Permission: "x", ResourceType: "y"}); err != nil || u != nil {
		t.Fatalf("UpdatePolicy(absent) = %+v, %v", u, err)
	}
	// Update로 (perm,resource) 충돌 → 23505.
	_, err = repo.UpdatePolicy(ctx, "fold-write", UpdateParams{Permission: "read", ResourceType: "document"})
	if err == nil || !isUniqueViolation(err) {
		t.Fatalf("expected 23505 on update clash, got %v", err)
	}

	// Delete.
	ok, err := repo.DeletePolicy(ctx, "doc-read")
	if err != nil || !ok {
		t.Fatalf("DeletePolicy = %v, %v", ok, err)
	}
	if again, err := repo.DeletePolicy(ctx, "doc-read"); err != nil || again {
		t.Fatalf("DeletePolicy(again) = %v, %v", again, err)
	}
}
