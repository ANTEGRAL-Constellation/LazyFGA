package idp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ── fake DBTX / Row / Rows (오류 분기 커버) ──

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

var errBoom = errors.New("db boom")

func TestRepo_ErrorBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("ListConnections query/scan/err", func(t *testing.T) {
		if _, err := NewRepo(&fakeDBTX{queryErr: errBoom}).ListConnections(ctx); !errors.Is(err, errBoom) {
			t.Errorf("query err = %v", err)
		}
		if _, err := NewRepo(&fakeDBTX{queryRows: &fakeRows{remaining: 1, scanErr: errBoom}}).ListConnections(ctx); !errors.Is(err, errBoom) {
			t.Errorf("scan err = %v", err)
		}
		if _, err := NewRepo(&fakeDBTX{queryRows: &fakeRows{errVal: errBoom}}).ListConnections(ctx); !errors.Is(err, errBoom) {
			t.Errorf("rows.Err = %v", err)
		}
	})

	t.Run("GetConnectionByID", func(t *testing.T) {
		got, err := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}).GetConnectionByID(ctx, "x")
		if got != nil || err != nil {
			t.Errorf("no rows → nil,nil got %v,%v", got, err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).GetConnectionByID(ctx, "x"); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("GetConnectionByProvider", func(t *testing.T) {
		got, err := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}).GetConnectionByProvider(ctx, "x")
		if got != nil || err != nil {
			t.Errorf("no rows → nil,nil got %v,%v", got, err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).GetConnectionByProvider(ctx, "x"); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("CreateConnection unique vs generic", func(t *testing.T) {
		unique := &pgconn.PgError{Code: "23505"}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: unique}}).CreateConnection(ctx, CreateConnectionInput{}); err != ErrDuplicateProvider {
			t.Errorf("unique → %v, want ErrDuplicateProvider", err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).CreateConnection(ctx, CreateConnectionInput{}); !errors.Is(err, errBoom) {
			t.Errorf("generic → %v", err)
		}
	})

	t.Run("UpdateConnection no-rows/err", func(t *testing.T) {
		got, err := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}).UpdateConnection(ctx, "x", ConnectionPatch{Enabled: boolp(true)})
		if got != nil || err != nil {
			t.Errorf("no rows → nil,nil got %v,%v", got, err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).UpdateConnection(ctx, "x", ConnectionPatch{}); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("DeleteConnection no-rows/err", func(t *testing.T) {
		ok, err := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}).DeleteConnection(ctx, "x")
		if ok || err != nil {
			t.Errorf("no rows → false,nil got %v,%v", ok, err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).DeleteConnection(ctx, "x"); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("ListRulesByConnection query/scan/err", func(t *testing.T) {
		if _, err := NewRepo(&fakeDBTX{queryErr: errBoom}).ListRulesByConnection(ctx, "c"); !errors.Is(err, errBoom) {
			t.Errorf("query err = %v", err)
		}
		if _, err := NewRepo(&fakeDBTX{queryRows: &fakeRows{remaining: 1, scanErr: errBoom}}).ListRulesByConnection(ctx, "c"); !errors.Is(err, errBoom) {
			t.Errorf("scan err = %v", err)
		}
		if _, err := NewRepo(&fakeDBTX{queryRows: &fakeRows{errVal: errBoom}}).ListRulesByConnection(ctx, "c"); !errors.Is(err, errBoom) {
			t.Errorf("rows.Err = %v", err)
		}
	})

	t.Run("GetRuleByID", func(t *testing.T) {
		got, err := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}).GetRuleByID(ctx, "r")
		if got != nil || err != nil {
			t.Errorf("no rows → nil,nil got %v,%v", got, err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).GetRuleByID(ctx, "r"); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("GetRulesByProvider query/scan/err", func(t *testing.T) {
		if _, err := NewRepo(&fakeDBTX{queryErr: errBoom}).GetRulesByProvider(ctx, "p"); !errors.Is(err, errBoom) {
			t.Errorf("query err = %v", err)
		}
		if _, err := NewRepo(&fakeDBTX{queryRows: &fakeRows{remaining: 1, scanErr: errBoom}}).GetRulesByProvider(ctx, "p"); !errors.Is(err, errBoom) {
			t.Errorf("scan err = %v", err)
		}
		if _, err := NewRepo(&fakeDBTX{queryRows: &fakeRows{errVal: errBoom}}).GetRulesByProvider(ctx, "p"); !errors.Is(err, errBoom) {
			t.Errorf("rows.Err = %v", err)
		}
	})

	t.Run("CreateRule scan err", func(t *testing.T) {
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).CreateRule(ctx, "c", CreateRuleInput{Match: json.RawMessage("[]")}); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("UpdateRule no-rows/err", func(t *testing.T) {
		got, err := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}).UpdateRule(ctx, "r", RulePatch{Priority: intp(1)})
		if got != nil || err != nil {
			t.Errorf("no rows → nil,nil got %v,%v", got, err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).UpdateRule(ctx, "r", RulePatch{}); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("DeleteRule no-rows/err", func(t *testing.T) {
		ok, err := NewRepo(&fakeDBTX{row: fakeRow{err: pgx.ErrNoRows}}).DeleteRule(ctx, "r")
		if ok || err != nil {
			t.Errorf("no rows → false,nil got %v,%v", ok, err)
		}
		if _, err := NewRepo(&fakeDBTX{row: fakeRow{err: errBoom}}).DeleteRule(ctx, "r"); !errors.Is(err, errBoom) {
			t.Errorf("err = %v", err)
		}
	})
}
