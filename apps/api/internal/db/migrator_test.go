package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestLoadMigrations_embedded(t *testing.T) {
	migs, err := loadMigrations(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migs) != 7 {
		t.Fatalf("got %d migrations, want 7", len(migs))
	}
	wantTags := []string{
		"0000_polite_ironclad", "0001_low_nextwave", "0002_greedy_hellion",
		"0003_skinny_miracleman", "0004_rainy_proteus", "0005_colorful_mystique",
		"0006_flaky_gunslinger",
	}
	wantWhen := []int64{1782657911623, 1782661414367, 1782662862897, 1782697146139, 1782698839908, 1782699454130, 1782750639051}
	for i, m := range migs {
		if m.tag != wantTags[i] {
			t.Errorf("migs[%d].tag = %q, want %q", i, m.tag, wantTags[i])
		}
		if m.when != wantWhen[i] {
			t.Errorf("migs[%d].when = %d, want %d", i, m.when, wantWhen[i])
		}
		// 해시는 원본 파일 바이트의 sha256 hex여야 한다.
		content, _ := migrationsFS.ReadFile("migrations/" + m.tag + ".sql")
		sum := sha256.Sum256(content)
		if m.hash != hex.EncodeToString(sum[:]) {
			t.Errorf("migs[%d].hash mismatch", i)
		}
	}
	// 0000은 breakpoint 없음 → 1문장. 0001은 breakpoint 2개 → 3문장.
	if len(migs[0].statements) != 1 {
		t.Errorf("0000 statements = %d, want 1", len(migs[0].statements))
	}
	if len(migs[1].statements) != 3 {
		t.Errorf("0001 statements = %d, want 3", len(migs[1].statements))
	}
}

func TestLoadMigrations_errors(t *testing.T) {
	t.Run("missing journal", func(t *testing.T) {
		if _, err := loadMigrations(fstest.MapFS{}, "migrations"); err == nil {
			t.Fatal("expected error for missing journal")
		}
	})
	t.Run("bad journal json", func(t *testing.T) {
		fsys := fstest.MapFS{"m/meta/_journal.json": &fstest.MapFile{Data: []byte("{not json")}}
		if _, err := loadMigrations(fsys, "m"); err == nil {
			t.Fatal("expected error for bad journal json")
		}
	})
	t.Run("missing sql file", func(t *testing.T) {
		fsys := fstest.MapFS{
			"m/meta/_journal.json": &fstest.MapFile{Data: []byte(`{"entries":[{"idx":0,"when":1,"tag":"0000_x"}]}`)},
		}
		if _, err := loadMigrations(fsys, "m"); err == nil {
			t.Fatal("expected error for missing sql file")
		}
	})
}

func TestMigrate_loadError(t *testing.T) {
	// fsys가 비어 있으면 loadMigrations가 실패하고 pool은 사용되지 않는다.
	m := &Migrator{fsys: fstest.MapFS{}, dir: "migrations", lockKey: advisoryLockKey}
	if err := m.Migrate(context.Background(), nil); err == nil {
		t.Fatal("expected load error")
	}
}

// ---- applyMigrations 단위 테스트(fake DB) ----

type fakeRow struct {
	tx *fakeTx
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.tx.queryErr != nil {
		return r.tx.queryErr
	}
	if r.tx.noRows {
		return pgx.ErrNoRows
	}
	*(dest[0].(**int64)) = r.tx.lastApplied
	return nil
}

type fakeTx struct {
	execErrOn   map[string]error
	execCalls   []string
	lastApplied *int64
	noRows      bool
	queryErr    error
	commitErr   error
	committed   bool
	rolledBack  bool
}

func (t *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.execCalls = append(t.execCalls, sql)
	for sub, err := range t.execErrOn {
		if strings.Contains(sql, sub) {
			return pgconn.CommandTag{}, err
		}
	}
	return pgconn.CommandTag{}, nil
}
func (t *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row { return &fakeRow{tx: t} }
func (t *fakeTx) Commit(context.Context) error {
	if t.commitErr != nil {
		return t.commitErr
	}
	t.committed = true
	return nil
}
func (t *fakeTx) Rollback(context.Context) error { t.rolledBack = true; return nil }

type fakePool struct {
	execErrOn map[string]error
	beginErr  error
	tx        *fakeTx
}

func (p *fakePool) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	for sub, err := range p.execErrOn {
		if strings.Contains(sql, sub) {
			return pgconn.CommandTag{}, err
		}
	}
	return pgconn.CommandTag{}, nil
}
func (p *fakePool) BeginTx(context.Context) (migrateTx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
}

func sampleMigs() []migrationFile {
	return []migrationFile{
		{tag: "a", when: 10, hash: "ha", statements: []string{"CREATE TABLE aaa (id int)"}},
		{tag: "b", when: 20, hash: "hb", statements: []string{"CREATE TABLE bbb (id int)", "   ", "CREATE INDEX bbb_idx ON bbb(id)"}},
		{tag: "c", when: 30, hash: "hc", statements: []string{"CREATE TABLE ccc (id int)"}},
	}
}

func countContaining(calls []string, sub string) int {
	n := 0
	for _, c := range calls {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

func TestApplyMigrations_freshAppliesAll(t *testing.T) {
	tx := &fakeTx{noRows: true}
	pool := &fakePool{tx: tx}
	if err := applyMigrations(context.Background(), pool, sampleMigs(), 42); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	if !tx.committed {
		t.Fatal("tx not committed")
	}
	// 락 1 + 각 문장 + insert 3. 공백 문장은 건너뛴다.
	if got := countContaining(tx.execCalls, "pg_advisory_xact_lock"); got != 1 {
		t.Errorf("advisory lock calls = %d, want 1", got)
	}
	if got := countContaining(tx.execCalls, "INSERT INTO drizzle"); got != 3 {
		t.Errorf("insert calls = %d, want 3", got)
	}
	if got := countContaining(tx.execCalls, "CREATE TABLE"); got != 3 {
		t.Errorf("create table calls = %d, want 3", got)
	}
	if got := countContaining(tx.execCalls, "CREATE INDEX"); got != 1 {
		t.Errorf("create index calls = %d, want 1", got)
	}
	// 첫 exec은 반드시 advisory lock이어야 한다.
	if !strings.Contains(tx.execCalls[0], "pg_advisory_xact_lock") {
		t.Errorf("first statement = %q, want advisory lock", tx.execCalls[0])
	}
}

func TestApplyMigrations_adoptionNoOp(t *testing.T) {
	max := int64(30)
	tx := &fakeTx{lastApplied: &max}
	pool := &fakePool{tx: tx}
	if err := applyMigrations(context.Background(), pool, sampleMigs(), 42); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	if got := countContaining(tx.execCalls, "INSERT INTO drizzle"); got != 0 {
		t.Errorf("insert calls = %d, want 0 (adoption no-op)", got)
	}
	if got := countContaining(tx.execCalls, "CREATE TABLE"); got != 0 {
		t.Errorf("create table calls = %d, want 0", got)
	}
	if !tx.committed {
		t.Fatal("tx not committed")
	}
}

func TestApplyMigrations_partial(t *testing.T) {
	last := int64(15) // a(10) 적용됨, b(20)/c(30) 적용
	tx := &fakeTx{lastApplied: &last}
	pool := &fakePool{tx: tx}
	if err := applyMigrations(context.Background(), pool, sampleMigs(), 42); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	if got := countContaining(tx.execCalls, "INSERT INTO drizzle"); got != 2 {
		t.Errorf("insert calls = %d, want 2", got)
	}
	if countContaining(tx.execCalls, "aaa") != 0 {
		t.Error("migration a should be skipped")
	}
	if countContaining(tx.execCalls, "bbb") == 0 || countContaining(tx.execCalls, "ccc") == 0 {
		t.Error("migrations b and c should be applied")
	}
}

func TestApplyMigrations_nullCreatedAtAppliesAll(t *testing.T) {
	// created_at이 NULL(행은 있으나 값 없음)이면 last=nil로 전체 적용.
	tx := &fakeTx{lastApplied: nil, noRows: false}
	pool := &fakePool{tx: tx}
	if err := applyMigrations(context.Background(), pool, sampleMigs(), 42); err != nil {
		t.Fatalf("applyMigrations: %v", err)
	}
	if got := countContaining(tx.execCalls, "INSERT INTO drizzle"); got != 3 {
		t.Errorf("insert calls = %d, want 3", got)
	}
}

func TestApplyMigrations_errorBranches(t *testing.T) {
	sentinel := errors.New("boom")
	tests := []struct {
		name string
		pool *fakePool
	}{
		{"create schema", &fakePool{execErrOn: map[string]error{"CREATE SCHEMA": sentinel}, tx: &fakeTx{noRows: true}}},
		{"create table", &fakePool{execErrOn: map[string]error{"CREATE TABLE IF NOT EXISTS drizzle": sentinel}, tx: &fakeTx{noRows: true}}},
		{"begin", &fakePool{beginErr: sentinel}},
		{"advisory lock", &fakePool{tx: &fakeTx{noRows: true, execErrOn: map[string]error{"pg_advisory_xact_lock": sentinel}}}},
		{"read last", &fakePool{tx: &fakeTx{queryErr: sentinel}}},
		{"apply statement", &fakePool{tx: &fakeTx{noRows: true, execErrOn: map[string]error{"aaa": sentinel}}}},
		{"insert bookkeeping", &fakePool{tx: &fakeTx{noRows: true, execErrOn: map[string]error{"INSERT INTO drizzle": sentinel}}}},
		{"commit", &fakePool{tx: &fakeTx{noRows: true, commitErr: sentinel}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := applyMigrations(context.Background(), tc.pool, sampleMigs(), 42)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !errors.Is(err, sentinel) {
				t.Fatalf("error not wrapping sentinel: %v", err)
			}
		})
	}
}
