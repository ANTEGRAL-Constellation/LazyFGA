package auditread

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── 순수 헬퍼 ──

func TestEscapeLike(t *testing.T) {
	if got := escapeLike(`a%b_c\d`); got != `a\%b\_c\\d` {
		t.Errorf("escapeLike = %q", got)
	}
	if got := escapeLike("plain"); got != "plain" {
		t.Errorf("escapeLike plain = %q", got)
	}
}

func TestCompactData(t *testing.T) {
	if got := string(compactData([]byte(`{"a": 1, "b": 2}`))); got != `{"a":1,"b":2}` {
		t.Errorf("compact = %q", got)
	}
	// 키 순서 보존(Postgres 정규화 순서 그대로): id 먼저(짧음), connectionId 나중.
	if got := string(compactData([]byte(`{"id": "x", "connectionId": "y"}`))); got != `{"id":"x","connectionId":"y"}` {
		t.Errorf("key order = %q", got)
	}
	// jsonb 바이너리 버전 바이트(0x01) 방어.
	withVer := append([]byte{0x01}, []byte(`{"a":1}`)...)
	if got := string(compactData(withVer)); got != `{"a":1}` {
		t.Errorf("version byte = %q", got)
	}
	if got := string(compactData(nil)); got != `{}` {
		t.Errorf("empty = %q", got)
	}
	// 무효 JSON → 원본 폴백.
	if got := string(compactData([]byte("notjson"))); got != "notjson" {
		t.Errorf("invalid fallback = %q", got)
	}
}

// ── fake Querier 오류 분기 ──

type fakeQuerier struct {
	rows pgx.Rows
	err  error
}

func (f *fakeQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return f.rows, f.err
}

type fakeRows struct {
	remaining int
	scanErr   error
	errVal    error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.errVal }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool {
	if r.remaining > 0 {
		r.remaining--
		return true
	}
	return false
}
func (r *fakeRows) Scan(...any) error      { return r.scanErr }
func (r *fakeRows) Values() ([]any, error) { return nil, nil }
func (r *fakeRows) RawValues() [][]byte    { return nil }
func (r *fakeRows) Conn() *pgx.Conn        { return nil }

var errBoom = errors.New("db boom")

func TestQuery_ErrorBranches(t *testing.T) {
	ctx := context.Background()
	if _, err := NewRepo(&fakeQuerier{err: errBoom}).Query(ctx, Query{Limit: 10}); !errors.Is(err, errBoom) {
		t.Errorf("query err = %v", err)
	}
	if _, err := NewRepo(&fakeQuerier{rows: &fakeRows{remaining: 1, scanErr: errBoom}}).Query(ctx, Query{Limit: 10}); !errors.Is(err, errBoom) {
		t.Errorf("scan err = %v", err)
	}
	if _, err := NewRepo(&fakeQuerier{rows: &fakeRows{errVal: errBoom}}).Query(ctx, Query{Limit: 10}); !errors.Is(err, errBoom) {
		t.Errorf("rows.Err = %v", err)
	}
}

// ── 통합 테스트(scratch DB) ──

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

func insertAudit(t *testing.T, pool *pgxpool.Pool, occurredAt time.Time, actor, action, data string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO audit_log (occurred_at, actor, action, data) VALUES ($1,$2,$3,$4::jsonb)`,
		occurredAt, actor, action, data); err != nil {
		t.Fatalf("insertAudit: %v", err)
	}
}

func TestQuery_Integration(t *testing.T) {
	pool := newMigratedScratchDB(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	insertAudit(t, pool, base, "admin", "token.create", `{"id":"x","connectionId":"y"}`)
	insertAudit(t, pool, base.Add(1*time.Second), "service:tok1", "token.revoke", `{"id":"a"}`)
	insertAudit(t, pool, base.Add(2*time.Second), "admin", "idp.rule.create", `{"id":"r1"}`)
	insertAudit(t, pool, base.Add(3*time.Second), "admin", "a_b", `{}`)
	insertAudit(t, pool, base.Add(4*time.Second), "admin", "axb", `{}`)

	t.Run("newest first + data passthrough (key order preserved)", func(t *testing.T) {
		res, err := repo.Query(ctx, Query{Limit: 50})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(res.Entries) != 5 {
			t.Fatalf("entries = %d", len(res.Entries))
		}
		if res.Entries[0].Action != "axb" || res.Entries[4].Action != "token.create" {
			t.Errorf("order wrong: %s ... %s", res.Entries[0].Action, res.Entries[4].Action)
		}
		last := res.Entries[4]
		if last.OccurredAt != "2026-07-02T12:00:00.000Z" || last.Actor != "admin" {
			t.Errorf("entry = %+v", last)
		}
		if string(last.Data) != `{"id":"x","connectionId":"y"}` {
			t.Errorf("data key order not preserved: %s", last.Data)
		}
	})

	t.Run("action exact match", func(t *testing.T) {
		res, _ := repo.Query(ctx, Query{Action: "token.create", Limit: 50})
		if len(res.Entries) != 1 || res.Entries[0].Action != "token.create" {
			t.Fatalf("exact = %+v", res.Entries)
		}
	})

	t.Run("action prefix with escaped underscore", func(t *testing.T) {
		// "a_*" prefix escapes `_` → matches only literal "a_b", not "axb".
		res, _ := repo.Query(ctx, Query{Action: "a_*", Limit: 50})
		if len(res.Entries) != 1 || res.Entries[0].Action != "a_b" {
			t.Fatalf("prefix escape = %+v", res.Entries)
		}
	})

	t.Run("actor filter", func(t *testing.T) {
		res, _ := repo.Query(ctx, Query{Actor: "service:tok1", Limit: 50})
		if len(res.Entries) != 1 || res.Entries[0].Action != "token.revoke" {
			t.Fatalf("actor = %+v", res.Entries)
		}
	})

	t.Run("from/to range", func(t *testing.T) {
		from := base.Add(1 * time.Second)
		to := base.Add(2 * time.Second)
		res, _ := repo.Query(ctx, Query{From: &from, To: &to, Limit: 50})
		if len(res.Entries) != 2 {
			t.Fatalf("range = %d entries", len(res.Entries))
		}
	})

	t.Run("keyset pagination", func(t *testing.T) {
		page1, _ := repo.Query(ctx, Query{Limit: 2})
		if len(page1.Entries) != 2 || page1.NextCursor == "" {
			t.Fatalf("page1 = %+v", page1)
		}
		cur, ok := decodeCursor(page1.NextCursor)
		if !ok {
			t.Fatal("nextCursor must decode")
		}
		page2, _ := repo.Query(ctx, Query{Limit: 2, Cursor: cur})
		if len(page2.Entries) != 2 {
			t.Fatalf("page2 = %d entries", len(page2.Entries))
		}
		// no overlap: page1 last action != page2 first action.
		if page1.Entries[1].Action == page2.Entries[0].Action {
			t.Error("keyset overlap")
		}
		// walk to the end.
		cur2, _ := decodeCursor(page2.NextCursor)
		page3, _ := repo.Query(ctx, Query{Limit: 2, Cursor: cur2})
		if len(page3.Entries) != 1 || page3.NextCursor != "" {
			t.Fatalf("page3 = %+v", page3)
		}
	})
}

func TestQuery_Integration_TieBreak(t *testing.T) {
	pool := newMigratedScratchDB(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	same := time.Date(2026, 7, 2, 12, 0, 0, 500_000_000, time.UTC)
	// 동일 occurred_at 2건 → id desc tie-break로 안정 페이지네이션.
	insertAudit(t, pool, same, "admin", "e.one", `{}`)
	insertAudit(t, pool, same, "admin", "e.two", `{}`)

	page1, _ := repo.Query(ctx, Query{Limit: 1})
	if len(page1.Entries) != 1 || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v", page1)
	}
	cur, _ := decodeCursor(page1.NextCursor)
	page2, _ := repo.Query(ctx, Query{Limit: 1, Cursor: cur})
	if len(page2.Entries) != 1 {
		t.Fatalf("page2 = %+v", page2)
	}
	if page1.Entries[0].Action == page2.Entries[0].Action {
		t.Error("tie-break failed: same row returned twice")
	}
}
