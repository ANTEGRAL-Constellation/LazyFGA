package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---- fake pgConn/Row/Rows (저장소 오류 분기 커버용) ----

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
}

func (f *fakeConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return f.rows, f.queryErr
}
func (f *fakeConn) QueryRow(context.Context, string, ...any) pgx.Row { return f.row }

func repo(c pgConn) *Repo { return &Repo{db: c} }

func TestFindByID_scanError(t *testing.T) {
	r := repo(&fakeConn{row: fakeRow{err: errBoom}})
	if _, err := r.FindByID(context.Background(), "x"); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
}

func TestFindByActionResource_scanError(t *testing.T) {
	r := repo(&fakeConn{row: fakeRow{err: errBoom}})
	if _, err := r.FindByActionResource(context.Background(), "p", "rt"); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
}

func TestListPolicies_errors(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		if _, err := repo(&fakeConn{queryErr: errBoom}).ListPolicies(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("scan error", func(t *testing.T) {
		if _, err := repo(&fakeConn{rows: &fakeRows{remaining: 1, scanErr: errBoom}}).ListPolicies(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("rows.Err", func(t *testing.T) {
		if _, err := repo(&fakeConn{rows: &fakeRows{remaining: 0, errVal: errBoom}}).ListPolicies(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestInsertUpdate_scanError(t *testing.T) {
	r := repo(&fakeConn{row: fakeRow{err: errBoom}})
	if _, err := r.InsertPolicy(context.Background(), contract.Policy{ID: "x"}); !errors.Is(err, errBoom) {
		t.Fatalf("insert err=%v", err)
	}
	if _, err := r.UpdatePolicy(context.Background(), "x", UpdateParams{}); !errors.Is(err, errBoom) {
		t.Fatalf("update err=%v", err)
	}
}

func TestDelete_generalError(t *testing.T) {
	r := repo(&fakeConn{row: fakeRow{err: errBoom}})
	if _, err := r.DeletePolicy(context.Background(), "x"); !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
}

// ---- 서비스 오류 분기(직접 호출) ----

func TestPolicyError_Error(t *testing.T) {
	if (&PolicyError{Status: 409, Detail: "boom"}).Error() != "boom" {
		t.Fatal("Error() should return detail")
	}
}

func TestCreatePolicy_lookupErrors(t *testing.T) {
	// findByActionResource가 에러를 내면 raw error 전파.
	store := &fakeStore{
		findByIDFn: func(context.Context, string) (*contract.Policy, error) { return nil, nil },
		findByARFn: func(context.Context, string, string) (*contract.Policy, error) { return nil, errBoom },
	}
	deps := Deps{Store: store, Model: nil}
	_, err := createPolicy(context.Background(), deps, createInput{ID: "ok-id", Permission: "read", ResourceType: "document"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err=%v", err)
	}
}

func TestEditPolicy_branches(t *testing.T) {
	existing := &contract.Policy{ID: "p1", Permission: "read", ResourceType: "document"}
	t.Run("findByID error", func(t *testing.T) {
		store := &fakeStore{findByIDFn: func(context.Context, string) (*contract.Policy, error) { return nil, errBoom }}
		if _, err := editPolicy(context.Background(), Deps{Store: store}, "p1", patchInput{}); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("existing gone (race) -> 422", func(t *testing.T) {
		store := &fakeStore{findByIDFn: func(context.Context, string) (*contract.Policy, error) { return nil, nil }}
		_, err := editPolicy(context.Background(), Deps{Store: store}, "p1", patchInput{})
		var pe *PolicyError
		if !errors.As(err, &pe) || pe.Status != 422 {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("findByActionResource error", func(t *testing.T) {
		store := &fakeStore{
			findByIDFn: func(context.Context, string) (*contract.Policy, error) { return existing, nil },
			findByARFn: func(context.Context, string, string) (*contract.Policy, error) { return nil, errBoom },
		}
		if _, err := editPolicy(context.Background(), Deps{Store: store}, "p1", patchInput{}); !errors.Is(err, errBoom) {
			t.Fatalf("err=%v", err)
		}
	})
}
