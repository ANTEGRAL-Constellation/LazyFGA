// Package model은 모델 발행/조회/diff을 담당한다(TS modules/model 포팅).
// publish는 validate → compile → writeAuthModel → 버전 저장 + current 포인터 갱신 → audit의
// 단일 절차이며, 조회는 current/versions/diff/versions:id 4개다. 교차 모듈(policy/pdp/permission)은
// Version.IR()로 타입 IR을 얻는다. 응답 바이트 parity를 위해 IRJSON은 JSONB 원본을 그대로 통과시킨다.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Version는 model_version 행이다. IRJSON은 JSONB 원본 바이트를 재마샬 없이 보존해(TS가
// JSONB→객체→stringify로 냈던 정규화 형태와 바이트 동일) 응답에 그대로 통과시킨다.
type Version struct {
	ID                   string
	AuthorizationModelID string
	IRJSON               json.RawMessage
	DSL                  string
	Note                 *string
	CreatedAt            time.Time
	CreatedBy            string
}

// IR은 저장된 IRJSON을 타입 ModelIR로 역직렬화한다(발행 시 검증됐으므로 정상 경로에선 실패 없음).
func (v *Version) IR() (*contract.ModelIR, error) {
	var ir contract.ModelIR
	if err := json.Unmarshal(v.IRJSON, &ir); err != nil {
		return nil, err
	}
	return &ir, nil
}

// PublishedVersion는 발행 성공 응답에 쓰이는 최소 필드(TS PublishedVersion).
type PublishedVersion struct {
	ID                   string
	AuthorizationModelID string
	CreatedAt            time.Time
}

// InsertParams는 model_version INSERT 입력이다.
type InsertParams struct {
	AuthorizationModelID string
	IRJSON               json.RawMessage
	DSL                  string
	Note                 *string
	CreatedBy            string
}

// Store는 발행/조회 핸들러가 필요로 하는 저장소 연산이다(테스트에서 fake 주입).
type Store interface {
	CurrentVersion(ctx context.Context) (*Version, error)
	ListVersions(ctx context.Context) ([]Version, error)
	GetVersion(ctx context.Context, id string) (*Version, error)
	InsertVersion(ctx context.Context, in InsertParams) (*PublishedVersion, error)
}

// pgConn은 저장소가 필요로 하는 pgx 연산이다(*pgxpool.Pool가 만족; 테스트 fake 주입점).
type pgConn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Repo는 pgx 기반 model_version/instance_config 저장소다.
type Repo struct {
	db pgConn
}

// NewRepo는 저장소를 만든다.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{db: pool} }

const versionColumns = `id, authorization_model_id, ir_json, dsl, note, created_at, created_by`

// CurrentVersion는 instance_config.current_model_version_id가 가리키는 버전을 반환한다.
// 포인터가 없거나(미발행) 대상 행이 없으면 (nil, nil).
func (r *Repo) CurrentVersion(ctx context.Context) (*Version, error) {
	var cur *string
	err := r.db.QueryRow(ctx, `SELECT current_model_version_id FROM instance_config LIMIT 1`).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, nil
	}
	return r.getByID(ctx, *cur)
}

// ListVersions는 모든 버전을 created_at 내림차순으로 반환한다.
func (r *Repo) ListVersions(ctx context.Context) ([]Version, error) {
	rows, err := r.db.Query(ctx, `SELECT `+versionColumns+` FROM model_version ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Version, 0)
	for rows.Next() {
		var v Version
		if err := scanVersionInto(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetVersion는 id로 단일 버전을 반환한다. malformed uuid는 Postgres의 uuid 파서(22P02)를
// 그대로 신뢰해 not-found로 매핑한다(§4.4-2; regex 선필터는 PG가 수용하는 무하이픈/중괄호
// 형식을 잘못 거절한다 — 리뷰 반영).
func (r *Repo) GetVersion(ctx context.Context, id string) (*Version, error) {
	v, err := r.getByID(ctx, id)
	if db.IsSQLState(err, "22P02") {
		return nil, nil
	}
	return v, err
}

func (r *Repo) getByID(ctx context.Context, id string) (*Version, error) {
	var v Version
	err := scanVersionInto(r.db.QueryRow(ctx, `SELECT `+versionColumns+` FROM model_version WHERE id = $1 LIMIT 1`, id), &v)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// InsertVersion는 model_version INSERT + instance_config 포인터 갱신을 단일 트랜잭션으로 수행한다.
// OpenFGA write는 이미 성공한 상태라 이 트랜잭션 실패는 고아 모델을 남길 수 있다(호출부가 감사).
func (r *Repo) InsertVersion(ctx context.Context, in InsertParams) (*PublishedVersion, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // commit 후에는 no-op.

	var pv PublishedVersion
	err = tx.QueryRow(ctx,
		`INSERT INTO model_version (authorization_model_id, ir_json, dsl, note, created_by)
		 VALUES ($1, $2::jsonb, $3, $4, $5)
		 RETURNING id, authorization_model_id, created_at`,
		in.AuthorizationModelID, string(in.IRJSON), in.DSL, in.Note, in.CreatedBy,
	).Scan(&pv.ID, &pv.AuthorizationModelID, &pv.CreatedAt)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE instance_config SET current_model_version_id = $1, updated_at = now() WHERE id = 'singleton'`,
		pv.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &pv, nil
}

// rowScanner는 pgx.Row와 pgx.Rows가 공통으로 갖는 Scan을 추상화한다.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanVersionInto(row rowScanner, v *Version) error {
	var ir []byte
	if err := row.Scan(&v.ID, &v.AuthorizationModelID, &ir, &v.DSL, &v.Note, &v.CreatedAt, &v.CreatedBy); err != nil {
		return err
	}
	v.IRJSON = json.RawMessage(ir)
	return nil
}
