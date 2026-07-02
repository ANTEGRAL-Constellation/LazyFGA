package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- fake DBTX / Row / Rows (오류 분기 커버용) ----

type fakeDBTX struct {
	queryErr  error
	queryRows pgx.Rows
	row       pgx.Row
	execErr   error
}

func (f *fakeDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return f.queryRows, f.queryErr
}
func (f *fakeDBTX) QueryRow(context.Context, string, ...any) pgx.Row { return f.row }
func (f *fakeDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, f.execErr
}

type fakeRow struct{ err error }

func (r fakeRow) Scan(...any) error { return r.err }

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

var errSentinel = errors.New("db boom")

func TestGenerateToken_randError(t *testing.T) {
	old := randReader
	defer func() { randReader = old }()
	randReader = errReader{}
	if _, _, err := GenerateToken(); err == nil {
		t.Fatal("expected error when rand source fails")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errSentinel }

func TestCreate_scanError(t *testing.T) {
	r := NewRepo(&fakeDBTX{row: fakeRow{err: errSentinel}})
	if _, err := r.Create(context.Background(), "n", "h"); !errors.Is(err, errSentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}

func TestList_errors(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		r := NewRepo(&fakeDBTX{queryErr: errSentinel})
		if _, err := r.List(context.Background()); !errors.Is(err, errSentinel) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("scan error in loop", func(t *testing.T) {
		r := NewRepo(&fakeDBTX{queryRows: &fakeRows{remaining: 1, scanErr: errSentinel}})
		if _, err := r.List(context.Background()); !errors.Is(err, errSentinel) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rows.Err after loop", func(t *testing.T) {
		r := NewRepo(&fakeDBTX{queryRows: &fakeRows{remaining: 0, errVal: errSentinel}})
		if _, err := r.List(context.Background()); !errors.Is(err, errSentinel) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRevoke_branches(t *testing.T) {
	t.Run("no rows -> false", func(t *testing.T) {
		r := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}})
		ok, err := r.Revoke(context.Background(), "id")
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v, want false,nil", ok, err)
		}
	})
	t.Run("general error", func(t *testing.T) {
		r := NewRepo(&fakeDBTX{row: fakeRow{err: errSentinel}})
		if _, err := r.Revoke(context.Background(), "id"); !errors.Is(err, errSentinel) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestFindActiveByHash_branches(t *testing.T) {
	t.Run("no rows -> nil,nil", func(t *testing.T) {
		r := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}})
		got, err := r.FindActiveByHash(context.Background(), "h")
		if err != nil || got != nil {
			t.Fatalf("got=%v err=%v, want nil,nil", got, err)
		}
	})
	t.Run("general error", func(t *testing.T) {
		r := NewRepo(&fakeDBTX{row: fakeRow{err: errSentinel}})
		if _, err := r.FindActiveByHash(context.Background(), "h"); !errors.Is(err, errSentinel) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestTouchLastUsed_execError(t *testing.T) {
	r := NewRepo(&fakeDBTX{execErr: errSentinel})
	if err := r.TouchLastUsed(context.Background(), "id"); !errors.Is(err, errSentinel) {
		t.Fatalf("err = %v", err)
	}
}
