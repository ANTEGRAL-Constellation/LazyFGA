package model

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- fake pgConn/Row/Rows/Tx (저장소 오류 분기 커버용, auth/repo_fake_test.go 패턴) ----

var errBoom = errors.New("db boom")

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

type fakeConn struct {
	queryErr error
	rows     pgx.Rows
	row      pgx.Row
	beginErr error
	tx       pgx.Tx
}

func (f *fakeConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return f.rows, f.queryErr
}
func (f *fakeConn) QueryRow(context.Context, string, ...any) pgx.Row { return f.row }
func (f *fakeConn) Begin(context.Context) (pgx.Tx, error)            { return f.tx, f.beginErr }

type fakeTx struct {
	insertRow pgx.Row
	execErr   error
	commitErr error
}

func (t *fakeTx) Begin(context.Context) (pgx.Tx, error) { return nil, nil }
func (t *fakeTx) Commit(context.Context) error          { return t.commitErr }
func (t *fakeTx) Rollback(context.Context) error        { return nil }
func (t *fakeTx) LargeObjects() pgx.LargeObjects        { return pgx.LargeObjects{} }
func (t *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}
func (t *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, t.execErr
}
func (t *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (t *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row        { return t.insertRow }
func (t *fakeTx) Conn() *pgx.Conn                                         { return nil }

func repo(c pgConn) *Repo { return &Repo{db: c} }

func TestCurrentVersion_errors(t *testing.T) {
	t.Run("no rows -> nil", func(t *testing.T) {
		r := repo(&fakeConn{row: fakeRow{err: pgx.ErrNoRows}})
		v, err := r.CurrentVersion(context.Background())
		if v != nil || err != nil {
			t.Fatalf("v=%v err=%v", v, err)
		}
	})
	t.Run("query error", func(t *testing.T) {
		r := repo(&fakeConn{row: fakeRow{err: errBoom}})
		if _, err := r.CurrentVersion(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestListVersions_errors(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		r := repo(&fakeConn{queryErr: errBoom})
		if _, err := r.ListVersions(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("scan error", func(t *testing.T) {
		r := repo(&fakeConn{rows: &fakeRows{remaining: 1, scanErr: errBoom}})
		if _, err := r.ListVersions(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("rows.Err", func(t *testing.T) {
		r := repo(&fakeConn{rows: &fakeRows{remaining: 0, errVal: errBoom}})
		if _, err := r.ListVersions(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestGetByID_scanError(t *testing.T) {
	r := repo(&fakeConn{row: fakeRow{err: errBoom}})
	if _, err := r.GetVersion(context.Background(), "11111111-1111-1111-1111-111111111111"); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
}

func TestInsertVersion_errors(t *testing.T) {
	t.Run("begin error", func(t *testing.T) {
		r := repo(&fakeConn{beginErr: errBoom})
		if _, err := r.InsertVersion(context.Background(), InsertParams{}); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("insert scan error", func(t *testing.T) {
		r := repo(&fakeConn{tx: &fakeTx{insertRow: fakeRow{err: errBoom}}})
		if _, err := r.InsertVersion(context.Background(), InsertParams{}); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("exec error", func(t *testing.T) {
		r := repo(&fakeConn{tx: &fakeTx{insertRow: fakeRow{}, execErr: errBoom}})
		if _, err := r.InsertVersion(context.Background(), InsertParams{}); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("commit error", func(t *testing.T) {
		r := repo(&fakeConn{tx: &fakeTx{insertRow: fakeRow{}, commitErr: errBoom}})
		if _, err := r.InsertVersion(context.Background(), InsertParams{}); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
}
