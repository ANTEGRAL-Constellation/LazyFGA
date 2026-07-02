package idp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/audit"
	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga/writeerror"
	"github.com/go-chi/chi/v5"
	fga "github.com/openfga/go-sdk"
)

// maxWebhookBody는 웹훅 raw body 상한이다(미인증 메모리 DoS 방지). 서명 검증 전 버퍼링 제한.
const maxWebhookBody = 256 * 1024

// ruleShapeError는 규칙 생성 shape 위반 422 메시지다(TS와 바이트 동일).
const ruleShapeError = "eventType, op(write|delete), tupleTemplate{user,relation,object}, match[], integer priority"

// Repo는 라우트가 필요로 하는 저장소 연산이다(consumer-owned interface; *DBRepo가 만족).
type Repo interface {
	ListConnections(ctx context.Context) ([]PublicConnection, error)
	GetConnectionByID(ctx context.Context, id string) (*PublicConnection, error)
	GetConnectionByProvider(ctx context.Context, provider string) (*Connection, error)
	CreateConnection(ctx context.Context, in CreateConnectionInput) (*PublicConnection, error)
	UpdateConnection(ctx context.Context, id string, patch ConnectionPatch) (*PublicConnection, error)
	DeleteConnection(ctx context.Context, id string) (bool, error)
	ListRulesByConnection(ctx context.Context, connID string) ([]StoredRule, error)
	GetRuleByID(ctx context.Context, ruleID string) (*StoredRule, error)
	GetRulesByProvider(ctx context.Context, provider string) ([]MappingRule, error)
	CreateRule(ctx context.Context, connID string, in CreateRuleInput) (*StoredRule, error)
	UpdateRule(ctx context.Context, ruleID string, patch RulePatch) (*StoredRule, error)
	DeleteRule(ctx context.Context, ruleID string) (bool, error)
}

// Gateway는 tuple write 연산이다(consumer-owned interface; openfga.Gateway가 만족).
type Gateway interface {
	Write(ctx context.Context, in openfga.WriteInput, opts ...openfga.WriteOption) error
}

// Deps는 idp 라우트의 의존성이다.
type Deps struct {
	Repo     Repo
	Gateway  Gateway
	Recorder audit.Recorder
	Auth     httpx.Authenticator
	Logger   *slog.Logger
	// Now는 서명 replay 윈도우용 클록(기본 time.Now).
	Now func() time.Time
}

type handlers struct {
	d Deps
}

// Mount는 idp 라우트를 마운트한다. 설정 CRUD는 admin 가드, 웹훅은 서명으로만 인증한다.
func Mount(r chi.Router, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	h := &handlers{d: d}
	r.Route("/idp", func(r chi.Router) {
		// 웹훅은 서명 검증 전 raw body를 전부 버퍼링하므로 크기를 제한한다.
		r.With(httpx.BodyLimit(maxWebhookBody)).Post("/webhook/{provider}", h.webhook)
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireRole(d.Auth, httpx.RoleAdmin))
			r.Post("/connections", h.createConnection)
			r.Get("/connections", h.listConnections)
			r.Put("/connections/{id}", h.updateConnection)
			r.Delete("/connections/{id}", h.deleteConnection)
			r.Get("/connections/{id}/rules", h.listRules)
			r.Post("/connections/{id}/rules", h.createRule)
			r.Put("/rules/{ruleId}", h.updateRule)
			r.Delete("/rules/{ruleId}", h.deleteRule)
		})
	})
}

// 응답 래퍼(단일 키; TS c.json({connection}) 등과 동일 shape).
type connectionEnvelope struct {
	Connection *PublicConnection `json:"connection"`
}
type connectionsEnvelope struct {
	Connections []PublicConnection `json:"connections"`
}
type ruleEnvelope struct {
	Rule *StoredRule `json:"rule"`
}
type rulesEnvelope struct {
	Rules []StoredRule `json:"rules"`
}

// actorOf는 요청 principal을 audit actor 문자열로 매핑한다.
func (h *handlers) actorOf(r *http.Request) string {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return audit.PrincipalActor(p)
}

// ── 웹훅(서명 인증, 토큰 불요) ─────────────────────────────────────────────────
func (h *handlers) webhook(w http.ResponseWriter, r *http.Request) {
	provider := httpx.URLParam(r, "provider")
	conn, err := h.d.Repo.GetConnectionByProvider(r.Context(), provider)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if conn == nil {
		httpx.WriteError(w, http.StatusNotFound, "unknown provider")
		return
	}
	if !conn.Enabled {
		httpx.WriteError(w, http.StatusForbidden, "connection disabled")
		return
	}
	// preset 키 = 연결에 저장된 키, 없으면 provider 이름으로 폴백(기존 zitadel 연결 하위호환).
	presetKey := provider
	if conn.Preset != nil {
		presetKey = *conn.Preset
	}
	preset, ok := presetByKey(presetKey)
	if !ok {
		// 서버 설정 오류 → 500. 미인증 단계 이전이라 DB audit엔 쓰지 않는다.
		h.d.Logger.Error("idp connection references unknown preset", "connectionId", conn.ID, "preset", presetKey)
		httpx.WriteError(w, http.StatusInternalServerError, "connection misconfigured (unknown preset)")
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.WriteError(w, http.StatusRequestEntityTooLarge, "payload too large")
			return
		}
		httpx.WriteHonoInternalError(w)
		return
	}
	if !VerifyWebhookSignature(preset.Signature, raw, r.Header, conn.SigningSecret, h.d.Now) {
		// 미인증 요청은 DB audit에 쓰지 않는다(audit_log amplification 방지). 앱 로그로만 남긴다.
		h.d.Logger.Warn("unauthorized webhook (signature verification failed)", "provider", provider)
		httpx.WriteError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}

	// 매핑 대상 아닌 이벤트 → 감사 흔적만 남기고 200 no-op. 관측성: 이벤트 타입을 함께 남긴다.
	ev := ExtractEvent(preset, body)
	if ev == nil {
		data := map[string]any{"provider": provider}
		if et, ok := readEventType(preset, body); ok {
			data["eventType"] = et
		}
		h.d.Recorder.Record("idp.webhook.no_events", data, "idp:"+provider)
		httpx.WriteJSON(w, http.StatusOK, ApplyResult{})
		return
	}

	rules, err := h.d.Repo.GetRulesByProvider(r.Context(), provider)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	deps := ApplyDeps{
		WriteTuple: func(op string, tuple RenderedTuple) (string, error) {
			var in openfga.WriteInput
			if op == "write" {
				in.Writes = []fga.TupleKey{{User: tuple.User, Relation: tuple.Relation, Object: tuple.Object}}
			} else {
				in.Deletes = []fga.TupleKeyWithoutCondition{{User: tuple.User, Relation: tuple.Relation, Object: tuple.Object}}
			}
			if err := h.d.Gateway.Write(r.Context(), in); err != nil {
				cls := writeerror.ClassifyWriteError(err, writeerror.Op(op))
				if cls.Idempotent {
					return "skipped", nil
				}
				return "", &WriteError{Transient: cls.Transient, Msg: err.Error()}
			}
			return "applied", nil
		},
		Audit: func(action string, data map[string]any) {
			h.d.Recorder.Record(action, data, "idp:"+provider)
		},
	}

	result, err := ApplyEvents([]IdpEvent{*ev}, rules, deps)
	if err != nil {
		var we *WriteError
		if errors.As(err, &we) && we.Transient {
			httpx.WriteError(w, http.StatusBadGateway, "upstream unavailable")
			return
		}
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

// ── 설정 CRUD(admin) ──────────────────────────────────────────────────────────
func (h *handlers) createConnection(w http.ResponseWriter, r *http.Request) {
	b, ok := parseJSONObject(r.Body)
	if !ok {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "non-empty provider and signingSecret are required")
		return
	}
	provider, pok := b["provider"].(string)
	secret, sok := b["signingSecret"].(string)
	if !pok || jsutil.TrimJS(provider) == "" || !sok || secret == "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "non-empty provider and signingSecret are required")
		return
	}
	var preset *string
	if raw, present := b["preset"]; present {
		ps, isStr := raw.(string)
		if !isStr || !presetKnown(ps) {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "unknown preset; known: "+strings.Join(presetKeys, ", "))
			return
		}
		preset = &ps
	}
	enabled := true
	if v, ok := b["enabled"].(bool); ok {
		enabled = v
	}

	conn, err := h.d.Repo.CreateConnection(r.Context(), CreateConnectionInput{
		Provider: provider, Preset: preset, SigningSecret: secret, Enabled: enabled,
	})
	if err != nil {
		if errors.Is(err, ErrDuplicateProvider) {
			httpx.WriteError(w, http.StatusConflict, "provider already exists")
			return
		}
		httpx.WriteHonoInternalError(w)
		return
	}
	h.d.Recorder.Record("idp.connection.create", map[string]any{"id": conn.ID, "provider": conn.Provider}, h.actorOf(r))
	httpx.WriteJSON(w, http.StatusCreated, connectionEnvelope{Connection: conn})
}

func (h *handlers) listConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := h.d.Repo.ListConnections(r.Context())
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, connectionsEnvelope{Connections: conns})
}

func (h *handlers) updateConnection(w http.ResponseWriter, r *http.Request) {
	id := httpx.URLParam(r, "id")
	existing, err := h.d.Repo.GetConnectionByID(r.Context(), id)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if existing == nil {
		httpx.WriteError(w, http.StatusNotFound, "connection not found")
		return
	}
	b := parseJSONObjectOrEmpty(r.Body)
	if raw, present := b["signingSecret"]; present {
		if s, ok := raw.(string); !ok || s == "" {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "signingSecret must be a non-empty string")
			return
		}
	}
	if raw, present := b["enabled"]; present {
		if _, ok := raw.(bool); !ok {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "enabled must be a boolean")
			return
		}
	}
	if raw, present := b["preset"]; present {
		if s, ok := raw.(string); !ok || !presetKnown(s) {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "unknown preset; known: "+strings.Join(presetKeys, ", "))
			return
		}
	}
	var patch ConnectionPatch
	if s, ok := b["preset"].(string); ok {
		patch.Preset = &s
	}
	if s, ok := b["signingSecret"].(string); ok {
		patch.SigningSecret = &s
	}
	if v, ok := b["enabled"].(bool); ok {
		patch.Enabled = &v
	}
	conn, err := h.d.Repo.UpdateConnection(r.Context(), id, patch)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if conn == nil {
		httpx.WriteError(w, http.StatusNotFound, "connection not found")
		return
	}
	h.d.Recorder.Record("idp.connection.update", map[string]any{"id": id}, h.actorOf(r))
	httpx.WriteJSON(w, http.StatusOK, connectionEnvelope{Connection: conn})
}

func (h *handlers) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id := httpx.URLParam(r, "id")
	ok, err := h.d.Repo.DeleteConnection(r.Context(), id)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "connection not found")
		return
	}
	h.d.Recorder.Record("idp.connection.delete", map[string]any{"id": id}, h.actorOf(r))
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) listRules(w http.ResponseWriter, r *http.Request) {
	id := httpx.URLParam(r, "id")
	existing, err := h.d.Repo.GetConnectionByID(r.Context(), id)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if existing == nil {
		httpx.WriteError(w, http.StatusNotFound, "connection not found")
		return
	}
	rules, err := h.d.Repo.ListRulesByConnection(r.Context(), id)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rulesEnvelope{Rules: rules})
}

func (h *handlers) createRule(w http.ResponseWriter, r *http.Request) {
	id := httpx.URLParam(r, "id")
	conn, err := h.d.Repo.GetConnectionByID(r.Context(), id)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if conn == nil {
		httpx.WriteError(w, http.StatusNotFound, "connection not found")
		return
	}
	b, ok := parseJSONObject(r.Body)
	if !ok {
		httpx.WriteError(w, http.StatusUnprocessableEntity, ruleShapeError)
		return
	}
	eventType, etOK := b["eventType"].(string)
	op, opOK := b["op"].(string)
	tt, ttOK := parseTupleTemplate(b["tupleTemplate"])
	_, matchOK := parseMatch(b["match"])
	priorityRaw, priorityPresent := b["priority"]
	if !etOK || !opOK || (op != "write" && op != "delete") || !ttOK || !matchOK || !validPriority(priorityRaw, priorityPresent) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, ruleShapeError)
		return
	}
	if ferr := fanOutError(conn, eventType, b["fanOut"], tt); ferr != "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, ferr)
		return
	}
	matchRaw, err := rawMatchJSON(b["match"])
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	in := CreateRuleInput{EventType: eventType, Match: matchRaw, TupleTemplate: tt, Op: op}
	if s, ok := b["fanOut"].(string); ok {
		in.FanOut = &s
	}
	if p, ok := priorityRaw.(float64); ok {
		in.Priority = int(p)
	}
	rule, err := h.d.Repo.CreateRule(r.Context(), id, in)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	h.d.Recorder.Record("idp.rule.create", map[string]any{"id": rule.ID, "connectionId": id}, h.actorOf(r))
	httpx.WriteJSON(w, http.StatusCreated, ruleEnvelope{Rule: rule})
}

func (h *handlers) updateRule(w http.ResponseWriter, r *http.Request) {
	ruleID := httpx.URLParam(r, "ruleId")
	// 기존 규칙을 먼저 로드해 병합본(eventType/template/fanOut)을 검증한다(부분 수정의 고아 {{item}} 방지).
	existing, err := h.d.Repo.GetRuleByID(r.Context(), ruleID)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if existing == nil {
		httpx.WriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	conn, err := h.d.Repo.GetConnectionByID(r.Context(), existing.ConnectionID)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	b := parseJSONObjectOrEmpty(r.Body)

	if raw, present := b["op"]; present {
		if s, ok := raw.(string); !ok || (s != "write" && s != "delete") {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "op must be write|delete")
			return
		}
	}
	if raw, present := b["match"]; present {
		if _, ok := parseMatchStrict(raw); !ok {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "invalid match[]")
			return
		}
	}
	if priorityRaw, present := b["priority"]; !validPriority(priorityRaw, present) {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "priority must be an integer")
		return
	}
	var ttPatch *TupleTemplate
	if raw, present := b["tupleTemplate"]; present {
		tt, ok := parseTupleTemplate(raw)
		if !ok {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "invalid tupleTemplate")
			return
		}
		ttPatch = &tt
	}
	if raw, present := b["fanOut"]; present && raw != nil {
		if s, ok := raw.(string); !ok || jsutil.TrimJS(s) == "" {
			httpx.WriteError(w, http.StatusUnprocessableEntity, "fanOut must be a non-empty string or null")
			return
		}
	}

	// 병합본으로 fanOut↔template↔preset 정합성을 검증(template만 바꿔도, fanOut만 비워도 잡힌다).
	mergedTemplate := existing.TupleTemplate
	if ttPatch != nil {
		mergedTemplate = *ttPatch
	}
	mergedEventType := existing.EventType
	if s, ok := b["eventType"].(string); ok {
		mergedEventType = s
	}
	mergedFanOut := mergeFanOut(b, existing.FanOut)
	if conn != nil {
		if ferr := fanOutError(conn, mergedEventType, mergedFanOut, mergedTemplate); ferr != "" {
			httpx.WriteError(w, http.StatusUnprocessableEntity, ferr)
			return
		}
	}

	patch := buildRulePatch(b, ttPatch)
	rule, err := h.d.Repo.UpdateRule(r.Context(), ruleID, patch)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if rule == nil {
		httpx.WriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	h.d.Recorder.Record("idp.rule.update", map[string]any{"ruleId": ruleID}, h.actorOf(r))
	httpx.WriteJSON(w, http.StatusOK, ruleEnvelope{Rule: rule})
}

func (h *handlers) deleteRule(w http.ResponseWriter, r *http.Request) {
	ruleID := httpx.URLParam(r, "ruleId")
	ok, err := h.d.Repo.DeleteRule(r.Context(), ruleID)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "rule not found")
		return
	}
	h.d.Recorder.Record("idp.rule.delete", map[string]any{"ruleId": ruleID}, h.actorOf(r))
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ──

// mergeFanOut은 PUT /rules 병합 검증용 fanOut 값을 계산한다(TS 삼항과 동일):
// JSON null → nil(=fanOut 없음), string → 그 값, 그 외/부재 → 기존 fanOut.
func mergeFanOut(b map[string]any, existing *string) any {
	if raw, present := b["fanOut"]; present {
		if raw == nil {
			return nil
		}
		if s, ok := raw.(string); ok {
			return s
		}
	}
	if existing == nil {
		return nil
	}
	return *existing
}

// buildRulePatch는 검증 통과 body로 RulePatch를 만든다.
func buildRulePatch(b map[string]any, ttPatch *TupleTemplate) RulePatch {
	var patch RulePatch
	if s, ok := b["eventType"].(string); ok {
		patch.EventType = &s
	}
	if raw, present := b["match"]; present {
		if _, ok := parseMatchStrict(raw); ok {
			if mj, err := rawMatchJSON(raw); err == nil {
				patch.Match = &mj
			}
		}
	}
	if ttPatch != nil {
		patch.TupleTemplate = ttPatch
	}
	if s, ok := b["op"].(string); ok && (s == "write" || s == "delete") {
		patch.Op = &s
	}
	if raw, present := b["fanOut"]; present {
		if raw == nil {
			patch.FanOutSet = true // null → 널 클리어.
		} else if s, ok := raw.(string); ok {
			patch.FanOutSet = true
			patch.FanOutValue = &s
		}
	}
	if p, ok := b["priority"].(float64); ok {
		pi := int(p)
		patch.Priority = &pi
	}
	return patch
}

// usesItem은 템플릿 3필드 중 하나라도 {{item}}을 참조하는지 본다.
func usesItem(tt TupleTemplate) bool {
	return strings.Contains(tt.User, "{{item}}") ||
		strings.Contains(tt.Relation, "{{item}}") ||
		strings.Contains(tt.Object, "{{item}}")
}

// fanOutError는 fan-out 설정을 검증한다(TS fanOutError 포팅). 문제 없으면 "".
func fanOutError(conn *PublicConnection, eventType string, fanOut any, tt TupleTemplate) string {
	s, isStr := fanOut.(string)
	if fanOut == nil || (isStr && s == "") {
		if usesItem(tt) {
			return "tuple template references {{item}} but no fanOut is set"
		}
		return ""
	}
	if !isStr {
		return "fanOut must be a non-empty string"
	}
	if !usesItem(tt) {
		return "fanOut requires the tuple template to reference {{item}}"
	}
	presetKey := conn.Provider
	if conn.Preset != nil {
		presetKey = *conn.Preset
	}
	if preset, ok := presetByKey(presetKey); ok {
		known := attributeNamesForEvent(preset, eventType)
		if !contains(known, s) {
			list := strings.Join(known, ", ")
			if list == "" {
				list = "none"
			}
			return fmt.Sprintf(`fanOut "%s" is not an attribute produced by preset for event "%s" (known: %s)`, s, eventType, list)
		}
	}
	return ""
}

// parseTupleTemplate은 raw가 user/relation/object 모두 문자열인 객체인지 검사·변환한다.
func parseTupleTemplate(raw any) (TupleTemplate, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return TupleTemplate{}, false
	}
	u, uok := m["user"].(string)
	rel, rok := m["relation"].(string)
	o, ook := m["object"].(string)
	if !uok || !rok || !ook {
		return TupleTemplate{}, false
	}
	return TupleTemplate{User: u, Relation: rel, Object: o}, true
}

// rawMatchJSON은 요청의 match 값을 JS 직렬화 raw 바이트로 만든다(부재/null → "[]").
// 잉여 키를 보존한다 — TS는 검증만 하고 원본 배열을 그대로 jsonb에 저장·에코한다.
func rawMatchJSON(raw any) (json.RawMessage, error) {
	if raw == nil {
		return json.RawMessage("[]"), nil
	}
	b, err := jsutil.MarshalJSON(raw)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// parseMatch는 생성용 match 파서다(부재/null → []; TS `b.match ?? []`).
func parseMatch(raw any) ([]MatchPredicate, bool) {
	if raw == nil {
		return make([]MatchPredicate, 0), true
	}
	return parseMatchStrict(raw)
}

// parseMatchStrict는 배열이 아니거나 원소 형식이 어긋나면 실패한다(수정용 검증).
func parseMatchStrict(raw any) ([]MatchPredicate, bool) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, false
	}
	out := make([]MatchPredicate, 0, len(arr))
	for _, el := range arr {
		m, ok := el.(map[string]any)
		if !ok {
			return nil, false
		}
		f, fok := m["field"].(string)
		eq, eok := m["equals"].(string)
		if !fok || !eok {
			return nil, false
		}
		out = append(out, MatchPredicate{Field: f, Equals: eq})
	}
	return out, true
}

// validPriority는 우선순위가 부재(=미지정)이거나 정수 number인지 본다(TS isValidPriority; null은 무효).
func validPriority(raw any, present bool) bool {
	if !present {
		return true
	}
	f, ok := raw.(float64)
	if !ok {
		return false
	}
	return !math.IsInf(f, 0) && f == math.Trunc(f)
}

// parseJSONObject는 body를 JSON 객체로 파싱한다. 무효/비-객체면 ok=false(TS `.catch(() => null)`).
func parseJSONObject(body io.Reader) (map[string]any, bool) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return m, true
}

// parseJSONObjectOrEmpty는 무효/비-객체면 빈 맵을 반환한다(TS `.catch(() => ({}))`).
func parseJSONObjectOrEmpty(body io.Reader) map[string]any {
	if m, ok := parseJSONObject(body); ok {
		return m
	}
	return map[string]any{}
}
