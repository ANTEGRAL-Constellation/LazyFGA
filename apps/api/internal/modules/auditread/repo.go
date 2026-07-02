package auditread

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Entry는 감사 조회 응답 항목이다(JSON shape은 contract.AuditEntry와 동일: id/occurredAt/actor/action/data).
// Data는 raw JSON(json.RawMessage)으로 두어 jsonb 키 순서를 보존한다 — contract.AuditEntry의
// map[string]any는 마샬 시 키를 정렬해 TS(예: idp.rule.create의 {id, connectionId} 순서)와 어긋난다.
type Entry struct {
	ID         string          `json:"id"`
	OccurredAt string          `json:"occurredAt"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	Data       json.RawMessage `json:"data"`
}

// Query는 감사 조회 파라미터다.
type Query struct {
	Action string // "" = 필터 없음
	Actor  string // "" = 필터 없음
	From   *time.Time
	To     *time.Time
	Limit  int
	Cursor *Cursor
}

// Result는 조회 결과(항목 + 다음 커서)다.
type Result struct {
	Entries    []Entry
	NextCursor string // "" = 없음
}

// Querier는 조회 저장소가 필요로 하는 pgx 연산이다(*pgxpool.Pool·pgx.Tx가 만족).
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// DBRepo는 audit_log 조회 저장소다.
type DBRepo struct {
	db Querier
}

// NewRepo는 조회 저장소를 만든다.
func NewRepo(db Querier) *DBRepo {
	return &DBRepo{db: db}
}

// Query는 최신순(occurred_at desc, id desc) keyset 페이지네이션 조회를 수행한다.
func (r *DBRepo) Query(ctx context.Context, q Query) (Result, error) {
	var conds []string
	var args []any

	if q.Action != "" {
		if strings.HasSuffix(q.Action, "*") {
			// trailing `*` → 접두 일치(LIKE; 와일드카드 문자는 이스케이프).
			args = append(args, escapeLike(q.Action[:len(q.Action)-1])+"%")
			conds = append(conds, fmt.Sprintf("action LIKE $%d", len(args)))
		} else {
			args = append(args, q.Action)
			conds = append(conds, fmt.Sprintf("action = $%d", len(args)))
		}
	}
	if q.Actor != "" {
		args = append(args, q.Actor)
		conds = append(conds, fmt.Sprintf("actor = $%d", len(args)))
	}
	if q.From != nil {
		args = append(args, *q.From)
		conds = append(conds, fmt.Sprintf("occurred_at >= $%d", len(args)))
	}
	if q.To != nil {
		args = append(args, *q.To)
		conds = append(conds, fmt.Sprintf("occurred_at <= $%d", len(args)))
	}
	if q.Cursor != nil {
		// desc 정렬에서 커서 다음 행: (occurred_at < c.at) OR (occurred_at = c.at AND id < c.id).
		args = append(args, q.Cursor.OccurredAt)
		ci := len(args)
		args = append(args, q.Cursor.ID)
		ii := len(args)
		conds = append(conds, fmt.Sprintf("(occurred_at < $%d OR (occurred_at = $%d AND id < $%d))", ci, ci, ii))
	}
	args = append(args, q.Limit+1)
	limitIdx := len(args)

	sql := "SELECT id, occurred_at, actor, action, data FROM audit_log"
	if len(conds) > 0 {
		sql += " WHERE " + strings.Join(conds, " AND ")
	}
	sql += fmt.Sprintf(" ORDER BY occurred_at DESC, id DESC LIMIT $%d", limitIdx)

	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()

	type raw struct {
		id         string
		occurredAt time.Time
		actor      string
		action     string
		data       []byte
	}
	fetched := make([]raw, 0, q.Limit+1)
	for rows.Next() {
		var x raw
		if err := rows.Scan(&x.id, &x.occurredAt, &x.actor, &x.action, &x.data); err != nil {
			return Result{}, err
		}
		fetched = append(fetched, x)
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}

	hasMore := len(fetched) > q.Limit
	if hasMore {
		fetched = fetched[:q.Limit]
	}
	entries := make([]Entry, 0, len(fetched))
	for _, x := range fetched {
		entries = append(entries, Entry{
			ID:         x.id,
			OccurredAt: isoMillis(x.occurredAt),
			Actor:      x.actor,
			Action:     x.action,
			Data:       compactData(x.data),
		})
	}
	nextCursor := ""
	if hasMore && len(fetched) > 0 {
		last := fetched[len(fetched)-1]
		nextCursor = encodeCursor(isoMillis(last.occurredAt), last.id)
	}
	return Result{Entries: entries, NextCursor: nextCursor}, nil
}

// escapeLike는 LIKE 접두 패턴용으로 `\ % _`를 이스케이프한다(TS `/[\\%_]/g` 대응).
func escapeLike(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\\' || c == '%' || c == '_' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// compactData는 jsonb 바이트에서 무의미 공백만 제거해(키 순서·값 보존) JS 컴팩트 형과 맞춘다.
// TS는 driver JSON.parse → Hono JSON.stringify로 Postgres 정규화 키 순서를 그대로 컴팩트 출력하는데,
// json.Compact도 순서를 재배열하지 않으므로 동일 바이트가 된다(문자열-값 데이터에 대해).
func compactData(raw []byte) json.RawMessage {
	// pgx jsonb 바이너리 포맷 방어: 선행 버전 바이트(0x01)는 JSON 시작 바이트가 아니므로 제거.
	if len(raw) > 0 && raw[0] == 0x01 {
		raw = raw[1:]
	}
	if len(raw) == 0 {
		return json.RawMessage("{}")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return json.RawMessage(raw)
	}
	return json.RawMessage(buf.Bytes())
}
