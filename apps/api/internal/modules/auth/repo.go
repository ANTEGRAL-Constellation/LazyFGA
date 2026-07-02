// Package auth는 service_token 저장소와 토큰 생성/해시 헬퍼를 제공한다.
// TS modules/auth/token.repo.ts + middleware/auth.ts의 토큰 부분을 포팅한다.
// 전체 토큰 CRUD 라우트는 LFGA-26에서 이 저장소를 소비한다.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// randReader는 토큰 랜덤 소스다(테스트 주입 지점). 기본은 crypto/rand.
var randReader io.Reader = rand.Reader

// ServiceToken은 service_token 행이다. 평문은 저장하지 않고 sha256만 보관한다.
type ServiceToken struct {
	ID         string
	Name       string
	TokenHash  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// DBTX는 저장소가 필요로 하는 pgx 연산이다(*pgxpool.Pool·pgx.Tx가 만족).
type DBTX interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Repo는 service_token 저장소다.
type Repo struct {
	db DBTX
}

// NewRepo는 저장소를 만든다.
func NewRepo(db DBTX) *Repo {
	return &Repo{db: db}
}

const tokenColumns = `id, name, token_hash, created_at, last_used_at, revoked_at`

// Sha256Hex는 문자열의 sha256 hex(소문자 64자)를 반환한다.
func Sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// GenerateToken은 랜덤 service token을 만든다. 평문은 호출자에 1회만 노출하고
// DB엔 sha256만 저장한다. 32바이트 랜덤을 base64url(패딩 없음)로 인코딩한다.
func GenerateToken() (plain, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(randReader, buf); err != nil {
		return "", "", fmt.Errorf("auth: generate token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, Sha256Hex(plain), nil
}

// Create는 새 토큰 행을 삽입하고 반환한다.
func (r *Repo) Create(ctx context.Context, name, tokenHash string) (*ServiceToken, error) {
	row := r.db.QueryRow(ctx,
		`INSERT INTO service_token (name, token_hash) VALUES ($1, $2) RETURNING `+tokenColumns,
		name, tokenHash)
	return scanToken(row)
}

// List는 모든 토큰을 created_at 내림차순으로 반환한다.
func (r *Repo) List(ctx context.Context) ([]ServiceToken, error) {
	rows, err := r.db.Query(ctx, `SELECT `+tokenColumns+` FROM service_token ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ServiceToken, 0)
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Revoke는 활성 토큰을 폐기 처리한다. 활성 토큰이 없으면 false(404).
func (r *Repo) Revoke(ctx context.Context, id string) (bool, error) {
	var revokedID string
	err := r.db.QueryRow(ctx,
		`UPDATE service_token SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL RETURNING id`, id).
		Scan(&revokedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	// malformed uuid는 PG 파서(22P02)를 신뢰해 not-found로 매핑한다(§4.4-2).
	if db.IsSQLState(err, "22P02") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// FindActiveByHash는 sha256 해시로 폐기되지 않은 토큰을 찾는다. 없으면 (nil, nil).
func (r *Repo) FindActiveByHash(ctx context.Context, tokenHash string) (*ServiceToken, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+tokenColumns+` FROM service_token WHERE token_hash = $1 AND revoked_at IS NULL LIMIT 1`,
		tokenHash)
	t, err := scanToken(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// TouchLastUsed는 last_used_at을 현재 시각으로 갱신한다(best-effort).
func (r *Repo) TouchLastUsed(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE service_token SET last_used_at = now() WHERE id = $1`, id)
	return err
}

// rowScanner는 pgx.Row와 pgx.Rows가 공통으로 갖는 Scan을 추상화한다.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanToken(row rowScanner) (*ServiceToken, error) {
	var t ServiceToken
	if err := row.Scan(&t.ID, &t.Name, &t.TokenHash, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
		return nil, err
	}
	return &t, nil
}
