package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateProvider는 provider 유니크 제약 위반(SQLSTATE 23505)을 나타낸다.
// 라우트가 이를 409로 매핑한다(TS 문자열 스니핑 대신 SQLSTATE 판정).
var ErrDuplicateProvider = errors.New("idp: duplicate provider")

// Connection은 idp_connection 행(시크릿 포함)이다. 웹훅 서명 검증 외부로 새지 않게 주의.
type Connection struct {
	ID            string
	Provider      string
	Preset        *string
	SigningSecret string
	Enabled       bool
}

// PublicConnection은 응답용(시크릿 제외)이다. signing_secret은 절대 노출하지 않는다.
type PublicConnection struct {
	ID       string  `json:"id"`
	Provider string  `json:"provider"`
	Preset   *string `json:"preset"`
	Enabled  bool    `json:"enabled"`
}

// StoredRule은 저장된 매핑 규칙(응답용)이다. FanOut은 null이면 생략(TS toRule과 동일).
type StoredRule struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connectionId"`
	EventType    string `json:"eventType"`
	// Match는 DB(jsonb)가 돌려준 정규화 바이트를 그대로 에코한다 — TS는 원소의 잉여 키도
	// 저장·반환하므로 구조체로 좁히면 parity가 깨진다(LFGA-26 리뷰 반영).
	Match         json.RawMessage `json:"match"`
	TupleTemplate TupleTemplate   `json:"tupleTemplate"`
	Op            string          `json:"op"`
	Priority      int             `json:"priority"`
	FanOut        *string         `json:"fanOut,omitempty"`
}

// DBTX는 저장소가 필요로 하는 pgx 연산이다(*pgxpool.Pool·pgx.Tx가 만족).
type DBTX interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DBRepo는 idp_connection / idp_mapping_rule 저장소다.
type DBRepo struct {
	db DBTX
}

// NewRepo는 저장소를 만든다.
func NewRepo(db DBTX) *DBRepo {
	return &DBRepo{db: db}
}

// rowScanner는 pgx.Row와 pgx.Rows가 공통으로 갖는 Scan을 추상화한다.
type rowScanner interface {
	Scan(dest ...any) error
}

const (
	connPublicCols = `id, provider, preset, enabled`
	connFullCols   = `id, provider, preset, signing_secret, enabled`
	ruleCols       = `id, connection_id, event_type, match, tuple_template, op, fan_out, priority`
)

// ── connections ──

// ListConnections는 모든 연결을 provider 오름차순으로 반환한다(시크릿 제외).
func (r *DBRepo) ListConnections(ctx context.Context) ([]PublicConnection, error) {
	rows, err := r.db.Query(ctx, `SELECT `+connPublicCols+` FROM idp_connection ORDER BY provider ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicConnection, 0)
	for rows.Next() {
		var c PublicConnection
		if err := rows.Scan(&c.ID, &c.Provider, &c.Preset, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConnectionByID는 id로 연결을 조회한다(시크릿 제외). 없으면 (nil, nil).
func (r *DBRepo) GetConnectionByID(ctx context.Context, id string) (*PublicConnection, error) {
	var c PublicConnection
	err := r.db.QueryRow(ctx, `SELECT `+connPublicCols+` FROM idp_connection WHERE id = $1 LIMIT 1`, id).
		Scan(&c.ID, &c.Provider, &c.Preset, &c.Enabled)
	if errors.Is(err, pgx.ErrNoRows) || db.IsSQLState(err, "22P02") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetConnectionByProvider는 웹훅 서명 검증용으로 provider의 연결(시크릿 포함)을 조회한다. 없으면 (nil, nil).
func (r *DBRepo) GetConnectionByProvider(ctx context.Context, provider string) (*Connection, error) {
	var c Connection
	err := r.db.QueryRow(ctx, `SELECT `+connFullCols+` FROM idp_connection WHERE provider = $1 LIMIT 1`, provider).
		Scan(&c.ID, &c.Provider, &c.Preset, &c.SigningSecret, &c.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateConnectionInput은 연결 생성 입력이다.
type CreateConnectionInput struct {
	Provider      string
	Preset        *string
	SigningSecret string
	Enabled       bool
}

// CreateConnection은 새 연결을 삽입한다. provider 중복이면 ErrDuplicateProvider.
func (r *DBRepo) CreateConnection(ctx context.Context, in CreateConnectionInput) (*PublicConnection, error) {
	var c PublicConnection
	err := r.db.QueryRow(ctx,
		`INSERT INTO idp_connection (provider, preset, signing_secret, enabled) VALUES ($1,$2,$3,$4) RETURNING `+connPublicCols,
		in.Provider, in.Preset, in.SigningSecret, in.Enabled).
		Scan(&c.ID, &c.Provider, &c.Preset, &c.Enabled)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicateProvider
		}
		return nil, err
	}
	return &c, nil
}

// ConnectionPatch는 부분 수정이다(nil 필드는 미변경). preset은 문자열로만 설정(널 클리어 없음).
type ConnectionPatch struct {
	Preset        *string
	SigningSecret *string
	Enabled       *bool
}

// UpdateConnection은 연결을 부분 수정한다. 없으면 (nil, nil).
func (r *DBRepo) UpdateConnection(ctx context.Context, id string, patch ConnectionPatch) (*PublicConnection, error) {
	sets := []string{"updated_at = now()"}
	var args []any
	if patch.Preset != nil {
		args = append(args, *patch.Preset)
		sets = append(sets, fmt.Sprintf("preset = $%d", len(args)))
	}
	if patch.SigningSecret != nil {
		args = append(args, *patch.SigningSecret)
		sets = append(sets, fmt.Sprintf("signing_secret = $%d", len(args)))
	}
	if patch.Enabled != nil {
		args = append(args, *patch.Enabled)
		sets = append(sets, fmt.Sprintf("enabled = $%d", len(args)))
	}
	args = append(args, id)
	sql := `UPDATE idp_connection SET ` + strings.Join(sets, ", ") +
		fmt.Sprintf(` WHERE id = $%d RETURNING `, len(args)) + connPublicCols
	var c PublicConnection
	err := r.db.QueryRow(ctx, sql, args...).Scan(&c.ID, &c.Provider, &c.Preset, &c.Enabled)
	if errors.Is(err, pgx.ErrNoRows) || db.IsSQLState(err, "22P02") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// DeleteConnection은 연결을 삭제한다(규칙은 FK cascade). 없으면 false.
func (r *DBRepo) DeleteConnection(ctx context.Context, id string) (bool, error) {
	var deletedID string
	err := r.db.QueryRow(ctx, `DELETE FROM idp_connection WHERE id = $1 RETURNING id`, id).Scan(&deletedID)
	if errors.Is(err, pgx.ErrNoRows) || db.IsSQLState(err, "22P02") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ── rules ──

// scanRule은 idp_mapping_rule 행 하나를 StoredRule로 스캔한다.
func scanRule(row rowScanner) (*StoredRule, error) {
	var s StoredRule
	var fanOut *string
	var matchRaw []byte
	if err := row.Scan(&s.ID, &s.ConnectionID, &s.EventType, &matchRaw, &s.TupleTemplate, &s.Op, &fanOut, &s.Priority); err != nil {
		return nil, err
	}
	if len(matchRaw) == 0 {
		matchRaw = []byte("[]")
	}
	// jsonb 텍스트는 콜론/콤마 뒤 공백을 포함한다 — TS(드라이버 parse → JSON.stringify)는
	// compact 형태이므로 동일하게 압축한다(키 순서는 jsonb 정규화 순서 그대로 유지).
	var compact bytes.Buffer
	if err := json.Compact(&compact, matchRaw); err != nil {
		return nil, err
	}
	s.Match = json.RawMessage(compact.Bytes())
	s.FanOut = fanOut
	// op 정규화: "delete"만 delete, 그 외 write(TS toRule parity).
	if s.Op != "delete" {
		s.Op = "write"
	}
	return &s, nil
}

// ListRulesByConnection은 연결의 규칙을 priority 오름차순으로 반환한다.
func (r *DBRepo) ListRulesByConnection(ctx context.Context, connID string) ([]StoredRule, error) {
	rows, err := r.db.Query(ctx, `SELECT `+ruleCols+` FROM idp_mapping_rule WHERE connection_id = $1 ORDER BY priority ASC`, connID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StoredRule, 0)
	for rows.Next() {
		s, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// GetRuleByID는 id로 규칙을 조회한다. 없으면 (nil, nil).
func (r *DBRepo) GetRuleByID(ctx context.Context, ruleID string) (*StoredRule, error) {
	s, err := scanRule(r.db.QueryRow(ctx, `SELECT `+ruleCols+` FROM idp_mapping_rule WHERE id = $1 LIMIT 1`, ruleID))
	if errors.Is(err, pgx.ErrNoRows) || db.IsSQLState(err, "22P02") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// GetRulesByProvider는 웹훅 처리용으로 provider의 모든 규칙을 priority 오름차순으로 반환한다.
func (r *DBRepo) GetRulesByProvider(ctx context.Context, provider string) ([]MappingRule, error) {
	rows, err := r.db.Query(ctx,
		`SELECT r.event_type, r.match, r.tuple_template, r.op, r.fan_out, r.priority
		 FROM idp_mapping_rule r JOIN idp_connection c ON r.connection_id = c.id
		 WHERE c.provider = $1 ORDER BY r.priority ASC`, provider)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MappingRule, 0)
	for rows.Next() {
		var m MappingRule
		var fanOut *string
		var matchRaw []byte
		if err := rows.Scan(&m.EventType, &matchRaw, &m.TupleTemplate, &m.Op, &fanOut, &m.Priority); err != nil {
			return nil, err
		}
		// 엔진용은 술어 필드만 필요하므로 raw에서 파싱한다(잉여 키는 무시 — TS matchRule과 동일).
		if len(matchRaw) > 0 {
			if err := json.Unmarshal(matchRaw, &m.Match); err != nil {
				return nil, err
			}
		}
		if m.Match == nil {
			m.Match = make([]MatchPredicate, 0)
		}
		m.FanOut = fanOut
		if m.Op != "delete" {
			m.Op = "write"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CreateRuleInput은 규칙 생성 입력이다.
type CreateRuleInput struct {
	EventType string
	// Match는 요청 body의 match 값을 JS 직렬화한 raw 바이트다(잉여 키 보존 — TS는 raw 배열을
	// 그대로 jsonb에 저장한다).
	Match         json.RawMessage
	TupleTemplate TupleTemplate
	Op            string
	FanOut        *string
	Priority      int
}

// CreateRule은 새 규칙을 삽입한다.
func (r *DBRepo) CreateRule(ctx context.Context, connID string, in CreateRuleInput) (*StoredRule, error) {
	tupleJSON, err := json.Marshal(in.TupleTemplate)
	if err != nil {
		return nil, err
	}
	match := in.Match
	if len(match) == 0 {
		match = json.RawMessage("[]")
	}
	return scanRule(r.db.QueryRow(ctx,
		`INSERT INTO idp_mapping_rule (connection_id, event_type, match, tuple_template, op, fan_out, priority)
		 VALUES ($1,$2,$3::jsonb,$4::jsonb,$5,$6,$7) RETURNING `+ruleCols,
		connID, in.EventType, string(match), string(tupleJSON), in.Op, in.FanOut, in.Priority))
}

// RulePatch는 규칙 부분 수정이다. FanOutSet=true면 FanOutValue로 설정(nil=널 클리어), false면 미변경.
type RulePatch struct {
	EventType *string
	// Match는 raw 바이트 패치다(잉여 키 보존; nil=미변경).
	Match         *json.RawMessage
	TupleTemplate *TupleTemplate
	Op            *string
	Priority      *int
	FanOutSet     bool
	FanOutValue   *string
}

// UpdateRule은 규칙을 부분 수정한다. 없으면 (nil, nil).
func (r *DBRepo) UpdateRule(ctx context.Context, ruleID string, patch RulePatch) (*StoredRule, error) {
	sets := []string{"updated_at = now()"}
	var args []any
	if patch.EventType != nil {
		args = append(args, *patch.EventType)
		sets = append(sets, fmt.Sprintf("event_type = $%d", len(args)))
	}
	if patch.Match != nil {
		args = append(args, string(*patch.Match))
		sets = append(sets, fmt.Sprintf("match = $%d::jsonb", len(args)))
	}
	if patch.TupleTemplate != nil {
		tj, err := json.Marshal(*patch.TupleTemplate)
		if err != nil {
			return nil, err
		}
		args = append(args, string(tj))
		sets = append(sets, fmt.Sprintf("tuple_template = $%d::jsonb", len(args)))
	}
	if patch.Op != nil {
		args = append(args, *patch.Op)
		sets = append(sets, fmt.Sprintf("op = $%d", len(args)))
	}
	if patch.FanOutSet {
		args = append(args, patch.FanOutValue)
		sets = append(sets, fmt.Sprintf("fan_out = $%d", len(args)))
	}
	if patch.Priority != nil {
		args = append(args, *patch.Priority)
		sets = append(sets, fmt.Sprintf("priority = $%d", len(args)))
	}
	args = append(args, ruleID)
	sql := `UPDATE idp_mapping_rule SET ` + strings.Join(sets, ", ") +
		fmt.Sprintf(` WHERE id = $%d RETURNING `, len(args)) + ruleCols
	s, err := scanRule(r.db.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) || db.IsSQLState(err, "22P02") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// DeleteRule은 규칙을 삭제한다. 없으면 false.
func (r *DBRepo) DeleteRule(ctx context.Context, ruleID string) (bool, error) {
	var deletedID string
	err := r.db.QueryRow(ctx, `DELETE FROM idp_mapping_rule WHERE id = $1 RETURNING id`, ruleID).Scan(&deletedID)
	if errors.Is(err, pgx.ErrNoRows) || db.IsSQLState(err, "22P02") {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// isUniqueViolation은 SQLSTATE 23505(unique_violation)인지 판정한다.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
