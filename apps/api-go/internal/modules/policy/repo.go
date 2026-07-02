// Package policy는 named policy(permission, resourceType) CRUD를 담당한다(TS modules/policy 포팅).
// (permission, resource_type)는 evaluate 조회 키이므로 전역 유일 — pre-check + DB UNIQUE(23505) backstop.
package policy

import (
	"context"
	"errors"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UpdateParams는 정책 수정 입력이다(conditionRef는 건드리지 않는다).
type UpdateParams struct {
	Permission   string
	ResourceType string
	Description  *string
}

// Store는 정책 핸들러가 필요로 하는 저장소 연산이다.
type Store interface {
	FindByID(ctx context.Context, id string) (*contract.Policy, error)
	FindByActionResource(ctx context.Context, permission, resourceType string) (*contract.Policy, error)
	ListPolicies(ctx context.Context) ([]contract.Policy, error)
	InsertPolicy(ctx context.Context, p contract.Policy) (*contract.Policy, error)
	UpdatePolicy(ctx context.Context, id string, patch UpdateParams) (*contract.Policy, error)
	DeletePolicy(ctx context.Context, id string) (bool, error)
}

// pgConn은 저장소가 필요로 하는 pgx 연산이다(*pgxpool.Pool가 만족; 테스트 fake 주입점).
type pgConn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repo는 pgx 기반 policy 저장소다.
type Repo struct {
	db pgConn
}

// NewRepo는 저장소를 만든다.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{db: pool} }

const policyColumns = `id, permission, resource_type, description, condition_ref`

// FindByID는 id로 정책을 찾는다. 없으면 (nil, nil).
func (r *Repo) FindByID(ctx context.Context, id string) (*contract.Policy, error) {
	return scanPolicy(r.db.QueryRow(ctx, `SELECT `+policyColumns+` FROM policy WHERE id = $1 LIMIT 1`, id))
}

// FindByActionResource는 (permission, resource_type)로 정책을 찾는다(evaluate 조회 키). 없으면 (nil, nil).
func (r *Repo) FindByActionResource(ctx context.Context, permission, resourceType string) (*contract.Policy, error) {
	return scanPolicy(r.db.QueryRow(ctx,
		`SELECT `+policyColumns+` FROM policy WHERE permission = $1 AND resource_type = $2 LIMIT 1`,
		permission, resourceType))
}

// ListPolicies는 모든 정책을 created_at 내림차순으로 반환한다.
func (r *Repo) ListPolicies(ctx context.Context) ([]contract.Policy, error) {
	rows, err := r.db.Query(ctx, `SELECT `+policyColumns+` FROM policy ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]contract.Policy, 0)
	for rows.Next() {
		p, err := scanPolicyInto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// InsertPolicy는 정책을 삽입하고 반환한다. UNIQUE 위반은 pgconn.PgError(23505)로 전파한다.
func (r *Repo) InsertPolicy(ctx context.Context, p contract.Policy) (*contract.Policy, error) {
	return scanPolicy(r.db.QueryRow(ctx,
		`INSERT INTO policy (id, permission, resource_type, description, condition_ref)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+policyColumns,
		p.ID, p.Permission, p.ResourceType, p.Description, p.ConditionRef))
}

// UpdatePolicy는 permission/resource_type/description + updated_at을 갱신한다(condition_ref 불변).
// 대상 행이 없으면 (nil, nil).
func (r *Repo) UpdatePolicy(ctx context.Context, id string, patch UpdateParams) (*contract.Policy, error) {
	return scanPolicy(r.db.QueryRow(ctx,
		`UPDATE policy SET permission = $2, resource_type = $3, description = $4, updated_at = now()
		 WHERE id = $1 RETURNING `+policyColumns,
		id, patch.Permission, patch.ResourceType, patch.Description))
}

// DeletePolicy는 정책을 삭제한다. 삭제됐으면 true, 없었으면 false.
func (r *Repo) DeletePolicy(ctx context.Context, id string) (bool, error) {
	var deletedID string
	err := r.db.QueryRow(ctx, `DELETE FROM policy WHERE id = $1 RETURNING id`, id).Scan(&deletedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// scanPolicy는 단일 행을 스캔한다. 행이 없으면 (nil, nil).
func scanPolicy(row rowScanner) (*contract.Policy, error) {
	p, err := scanPolicyInto(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func scanPolicyInto(row rowScanner) (*contract.Policy, error) {
	var p contract.Policy
	if err := row.Scan(&p.ID, &p.Permission, &p.ResourceType, &p.Description, &p.ConditionRef); err != nil {
		return nil, err
	}
	return &p, nil
}
