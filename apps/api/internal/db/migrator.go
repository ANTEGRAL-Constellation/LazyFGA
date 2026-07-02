package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql migrations/meta/_journal.json
var migrationsFS embed.FS

// statementBreakpoint는 Drizzle이 한 파일 내 문장 경계를 표기하는 마커다.
const statementBreakpoint = "--> statement-breakpoint"

// advisoryLockKey는 마이그레이션 직렬화용 고정 키다("lazyfga" ASCII).
// 다중 인스턴스가 동시에 부팅해도 apply가 직렬화된다(가산 하드닝; 단일 인스턴스 동작 불변).
const advisoryLockKey int64 = 0x6C617A79666761

const (
	createSchemaSQL = `CREATE SCHEMA IF NOT EXISTS drizzle`
	createTableSQL  = `CREATE TABLE IF NOT EXISTS drizzle.__drizzle_migrations (id SERIAL PRIMARY KEY, hash text NOT NULL, created_at bigint)`
	selectLastSQL   = `SELECT created_at FROM drizzle.__drizzle_migrations ORDER BY created_at DESC LIMIT 1`
	insertSQL       = `INSERT INTO drizzle.__drizzle_migrations (hash, created_at) VALUES ($1, $2)`
)

// journal은 Drizzle meta/_journal.json 구조다.
type journal struct {
	Version string         `json:"version"`
	Dialect string         `json:"dialect"`
	Entries []journalEntry `json:"entries"`
}

type journalEntry struct {
	Idx         int    `json:"idx"`
	Version     string `json:"version"`
	When        int64  `json:"when"`
	Tag         string `json:"tag"`
	Breakpoints bool   `json:"breakpoints"`
}

// migrationFile은 적용 단위다: 원본 해시 + 문장 목록 + 폴더 밀리초.
type migrationFile struct {
	tag        string
	when       int64
	hash       string
	statements []string
}

// execer는 Exec만 필요한 최소 인터페이스다(*pgxpool.Pool, pgx.Tx가 만족).
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// migrateTx는 마이그레이션 트랜잭션에 필요한 연산이다(pgx.Tx가 만족).
type migrateTx interface {
	execer
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// migratePool은 스키마 보장 Exec + 트랜잭션 시작을 추상화한다(테스트 주입 지점).
type migratePool interface {
	execer
	BeginTx(ctx context.Context) (migrateTx, error)
}

// poolAdapter는 *pgxpool.Pool을 migratePool로 감싼다.
type poolAdapter struct{ pool *pgxpool.Pool }

func (a poolAdapter) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return a.pool.Exec(ctx, sql, args...)
}

func (a poolAdapter) BeginTx(ctx context.Context) (migrateTx, error) {
	return a.pool.Begin(ctx)
}

// Migrator는 임베드된 SQL/journal로 drizzle 호환 부트키핑을 수행한다.
type Migrator struct {
	fsys    fs.FS
	dir     string
	lockKey int64
}

// NewMigrator는 임베드된 마이그레이션으로 마이그레이터를 만든다.
func NewMigrator() *Migrator {
	return &Migrator{fsys: migrationsFS, dir: "migrations", lockKey: advisoryLockKey}
}

// loadMigrations는 journal을 읽고 각 파일의 sha256(원본) 해시와 문장 분할을 계산한다.
// 해시는 Drizzle과 동일하게 파일 원문(마커·공백 포함) 바이트의 sha256 hex다.
func loadMigrations(fsys fs.FS, dir string) ([]migrationFile, error) {
	journalBytes, err := fs.ReadFile(fsys, dir+"/meta/_journal.json")
	if err != nil {
		return nil, fmt.Errorf("db: read journal: %w", err)
	}
	var j journal
	if err := json.Unmarshal(journalBytes, &j); err != nil {
		return nil, fmt.Errorf("db: parse journal: %w", err)
	}

	migs := make([]migrationFile, 0, len(j.Entries))
	for _, entry := range j.Entries {
		content, err := fs.ReadFile(fsys, dir+"/"+entry.Tag+".sql")
		if err != nil {
			return nil, fmt.Errorf("db: read migration %s: %w", entry.Tag, err)
		}
		sum := sha256.Sum256(content)
		migs = append(migs, migrationFile{
			tag:        entry.Tag,
			when:       entry.When,
			hash:       hex.EncodeToString(sum[:]),
			statements: strings.Split(string(content), statementBreakpoint),
		})
	}
	return migs, nil
}

// Migrate는 임베드된 마이그레이션을 적용한다. 멱등이며 동시 부팅에 안전하다(advisory lock).
// Drizzle postgres-js 마이그레이터 의미를 재현한다:
//  1. drizzle 스키마·부트키핑 테이블 보장(트랜잭션 밖, IF NOT EXISTS라 동시성 안전).
//  2. 단일 트랜잭션의 첫 문장으로 pg_advisory_xact_lock 획득.
//  3. 같은 트랜잭션 안에서 last(created_at 최대) 조회.
//  4. journal 순서대로 last 부재 또는 when > last면 문장 적용 + 부트키핑 INSERT. 커밋이 락 해제.
func (m *Migrator) Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migs, err := loadMigrations(m.fsys, m.dir)
	if err != nil {
		return err
	}
	return applyMigrations(ctx, poolAdapter{pool: pool}, migs, m.lockKey)
}

// applyMigrations는 drizzle 호환 적용 본체다(인터페이스 기반으로 테스트 가능).
func applyMigrations(ctx context.Context, pool migratePool, migs []migrationFile, lockKey int64) error {
	if _, err := pool.Exec(ctx, createSchemaSQL); err != nil {
		return fmt.Errorf("db: create drizzle schema: %w", err)
	}
	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		return fmt.Errorf("db: create bookkeeping table: %w", err)
	}

	tx, err := pool.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("db: begin migration tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 커밋 후에는 no-op.

	// 첫 문장: 동시 부팅 직렬화를 위한 세션 없는 트랜잭션 락.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return fmt.Errorf("db: acquire advisory lock: %w", err)
	}

	last, err := readLastApplied(ctx, tx)
	if err != nil {
		return err
	}

	for _, mig := range migs {
		if last != nil && mig.when <= *last {
			continue
		}
		for _, stmt := range mig.statements {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			// simple protocol: 원문 DDL(후행 세미콜론/개행 포함)을 그대로 실행.
			if _, err := tx.Exec(ctx, stmt, pgx.QueryExecModeSimpleProtocol); err != nil {
				return fmt.Errorf("db: apply migration %s: %w", mig.tag, err)
			}
		}
		if _, err := tx.Exec(ctx, insertSQL, mig.hash, mig.when); err != nil {
			return fmt.Errorf("db: record migration %s: %w", mig.tag, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit migration tx: %w", err)
	}
	return nil
}

// readLastApplied는 부트키핑 테이블에서 마지막 적용 created_at을 반환한다.
// 행이 없으면 nil, created_at이 NULL이어도 nil(=전체 재적용, Drizzle Number(null)=0과 동일).
func readLastApplied(ctx context.Context, tx migrateTx) (*int64, error) {
	var ca *int64
	err := tx.QueryRow(ctx, selectLastSQL).Scan(&ca)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("db: read last migration: %w", err)
	default:
		return ca, nil
	}
}
