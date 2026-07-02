package democli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	fga "github.com/openfga/go-sdk"
	fgaclient "github.com/openfga/go-sdk/client"
)

type fakeExec struct {
	reqs []fgaclient.ClientWriteRequest
	err  error
}

func (f *fakeExec) exec(_ context.Context, req fgaclient.ClientWriteRequest) error {
	f.reqs = append(f.reqs, req)
	return f.err
}

func TestSDKTupleGateway_write(t *testing.T) {
	ex := &fakeExec{}
	factoryCalls := 0
	g := &SDKTupleGateway{
		apiURL: "http://x",
		cache:  map[string]tupleExecutor{},
		newExec: func(_, _ string) (tupleExecutor, error) {
			factoryCalls++
			return ex, nil
		},
	}
	tup := Tuple{User: "user:a", Relation: "member", Object: "team:eng"}
	if err := g.Write(context.Background(), "store-1", tup); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(ex.reqs) != 1 || len(ex.reqs[0].Writes) != 1 {
		t.Fatalf("expected one write request with one tuple, got %+v", ex.reqs)
	}
	w := ex.reqs[0].Writes[0]
	if w.User != tup.User || w.Relation != tup.Relation || w.Object != tup.Object {
		t.Errorf("write tuple = %+v, want %+v", w, tup)
	}

	// 같은 store id 재사용 → executor 캐시(factory 1회만).
	if err := g.Delete(context.Background(), "store-1", tup); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if factoryCalls != 1 {
		t.Errorf("factory called %d times, want 1 (cached per store id)", factoryCalls)
	}
	if len(ex.reqs) != 2 || len(ex.reqs[1].Deletes) != 1 {
		t.Fatalf("expected a delete request, got %+v", ex.reqs)
	}
}

func TestSDKTupleGateway_factoryError(t *testing.T) {
	g := &SDKTupleGateway{
		apiURL: "http://x",
		cache:  map[string]tupleExecutor{},
		newExec: func(_, _ string) (tupleExecutor, error) {
			return nil, errors.New("bad url")
		},
	}
	if err := g.Write(context.Background(), "s", Tuple{}); err == nil || !strings.Contains(err.Error(), "bad url") {
		t.Fatalf("Write err = %v, want bad url", err)
	}
	if err := g.Delete(context.Background(), "s", Tuple{}); err == nil || !strings.Contains(err.Error(), "bad url") {
		t.Fatalf("Delete err = %v, want bad url", err)
	}
}

func TestDefaultTupleExecutor_constructs(t *testing.T) {
	// 프로덕션 팩토리 글루: 유효한 URL+ULID store id면 executor를 만든다(네트워크 호출 없음).
	ex, err := defaultTupleExecutor("http://localhost:8080", "01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("defaultTupleExecutor: %v", err)
	}
	if ex == nil {
		t.Fatal("executor is nil")
	}
}

// TestSDKExecutor_writesToServer는 프로덕션 sdkExecutor.exec가 store-바인딩 write를 실제 HTTP로
// 내보내는지 fake OpenFGA(httptest)로 확인한다 — go-sdk 글루 경로를 커버한다.
func TestSDKExecutor_writesToServer(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()

	const storeID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	ex, err := defaultTupleExecutor(srv.URL, storeID)
	if err != nil {
		t.Fatalf("defaultTupleExecutor: %v", err)
	}
	err = ex.exec(context.Background(), fgaclient.ClientWriteRequest{
		Writes: []fga.TupleKey{{User: "user:a", Relation: "member", Object: "team:eng"}},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !strings.Contains(gotPath, storeID) || !strings.HasSuffix(gotPath, "/write") {
		t.Errorf("unexpected write path %q", gotPath)
	}
}

func TestDefaultTupleExecutor_invalidStoreID(t *testing.T) {
	// 잘못된(비-ULID) store id → go-sdk 클라이언트 생성 실패(오류 반환 분기 커버).
	if _, err := defaultTupleExecutor("http://localhost:8080", "not-a-ulid"); err == nil {
		t.Fatal("expected error for invalid store id")
	}
}

func TestNewSDKTupleGateway_defaults(t *testing.T) {
	g := NewSDKTupleGateway("http://localhost:8080")
	if g.apiURL != "http://localhost:8080" || g.newExec == nil || g.cache == nil {
		t.Fatalf("gateway not wired: %+v", g)
	}
}

// ── NewPgxStoreID 통합 테스트(scratch DB) ────────────────────────────────────────

func newScratchDB(t *testing.T) *pgxpool.Pool {
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
	name := "lazyfga_test_" + hex.EncodeToString(b)
	withDB := func(db string) string {
		u, _ := url.Parse(raw)
		u.Path = "/" + db
		return u.String()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, withDB("postgres"))
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch: %v", err)
	}
	_ = admin.Close(ctx)

	pool, err := db.Connect(context.Background(), withDB(name))
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	if err := db.NewMigrator().Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
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
		_, _ = a.Exec(dctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		_, _ = a.Exec(dctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, name))
	})
	return pool
}

func TestNewPgxStoreID_integration(t *testing.T) {
	pool := newScratchDB(t)
	ctx := context.Background()
	read := NewPgxStoreID(pool)

	// 미부트스트랩(instance_config 비어 있음) → "".
	got, err := read(ctx)
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if got != "" {
		t.Fatalf("empty instance_config = %q, want \"\"", got)
	}

	// store id 저장 후 읽기.
	if _, err := pool.Exec(ctx,
		`INSERT INTO instance_config (id, openfga_store_id) VALUES ('singleton', $1)`, "store-42"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err = read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "store-42" {
		t.Fatalf("store id = %q, want store-42", got)
	}
}
