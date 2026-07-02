// Package db는 pgx 풀 연결과 drizzle 호환 마이그레이터를 제공한다.
// TS db/client.ts + db/migrate.ts를 포팅하되, 기존 Drizzle 부트키핑 테이블을
// 무변경으로 채택(adopt)하도록 마이그레이터를 재구현한다.
package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// maxConns는 TS postgres 클라이언트의 max:10과 동일하다.
const maxConns = 10

// Connect는 pgx 풀을 만든다. 풀은 지연 연결(lazy)이므로 DB가 아직 안 떠 있어도
// 즉시 성공한다 — 실제 연결 실패는 Migrate 등 첫 쿼리에서 표면화된다(degraded 부팅용).
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	// 멱등 마이그레이션의 "already exists" 류 NOTICE는 무시(부팅 로그 정리).
	cfg.ConnConfig.OnNotice = func(*pgconn.PgConn, *pgconn.Notice) {}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Ping은 Postgres 연결 헬스를 `select 1`로 확인한다. 실패 시 false(오류 삼킴).
func Ping(ctx context.Context, pool *pgxpool.Pool) bool {
	if pool == nil {
		return false
	}
	var one int
	if err := pool.QueryRow(ctx, "select 1").Scan(&one); err != nil {
		return false
	}
	return true
}

// IsSQLState는 err가 주어진 SQLSTATE 코드의 Postgres 오류인지 판정한다.
// 예: "22P02"(invalid_text_representation — malformed uuid 등), "23505"(unique_violation).
func IsSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
