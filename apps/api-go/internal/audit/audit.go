// Package audit는 변경 감사 기록을 담당한다(TS modules/audit/audit.ts 포팅).
// DB(audit_log)에 비차단으로 적재하며, 감사 실패가 감사 대상 작업을 절대 깨지 않는다
// (fire-and-forget + panic 복구 + 오류 로깅). LFGA-25/26은 이 Recorder를 소비만 하고,
// 읽기/조회 측은 LFGA-26이 소유한다.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/jackc/pgx/v5/pgconn"
)

// Recorder는 감사 기록 인터페이스다. 소비자는 이 인터페이스에만 의존한다.
type Recorder interface {
	Record(action string, data map[string]any, actor string)
}

// execer는 감사 삽입에 필요한 Exec만 추린 인터페이스다(*pgxpool.Pool가 만족).
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

const insertAuditSQL = `INSERT INTO audit_log (action, data, actor) VALUES ($1, $2::jsonb, $3)`

// DBRecorder는 audit_log에 비차단 삽입하는 DB 기반 구현이다.
type DBRecorder struct {
	db       execer
	logger   *slog.Logger
	dispatch func(func()) // 비동기 디스패치(테스트 주입 지점). 기본은 goroutine.
}

// NewDBRecorder는 DB 기반 감사 기록기를 만든다.
func NewDBRecorder(db execer, logger *slog.Logger) *DBRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &DBRecorder{
		db:       db,
		logger:   logger,
		dispatch: func(fn func()) { go fn() },
	}
}

// Record는 감사 이벤트를 비차단으로 적재한다. 호출자를 절대 막거나 깨지 않는다.
// 시그니처는 하위호환: actor가 빈 문자열이면 "system", data가 nil이면 "{}"로 저장한다.
func (r *DBRecorder) Record(action string, data map[string]any, actor string) {
	// 디스패치 이전 단계의 동기 패닉까지 흡수(감사가 절대 호출자를 깨지 않는다는 보장 유지).
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error("audit record panicked", "action", action, "err", rec)
		}
	}()
	if data == nil {
		data = map[string]any{}
	}
	if actor == "" {
		actor = "system"
	}
	r.dispatch(func() {
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Error("audit insert panicked", "action", action, "err", rec)
			}
		}()
		payload, err := json.Marshal(data)
		if err != nil {
			r.logger.Error("audit marshal failed", "action", action, "err", err)
			return
		}
		if _, err := r.db.Exec(context.Background(), insertAuditSQL, action, string(payload), actor); err != nil {
			r.logger.Error("audit insert failed", "action", action, "err", err)
		}
	})
}

// PrincipalActor는 principal을 audit actor 문자열로 매핑한다.
func PrincipalActor(p httpx.Principal) string {
	if p.Role == httpx.RoleAdmin {
		return "admin"
	}
	if p.TokenID != "" {
		return "service:" + p.TokenID
	}
	return "service"
}
