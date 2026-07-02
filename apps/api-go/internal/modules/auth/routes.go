package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/antegral-constellation/lazyfga/api/internal/jsontime"
	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
	"github.com/go-chi/chi/v5"
)

// TokenRepo는 토큰 라우트가 필요로 하는 저장소 연산이다(*Repo가 만족).
type TokenRepo interface {
	Create(ctx context.Context, name, tokenHash string) (*ServiceToken, error)
	List(ctx context.Context) ([]ServiceToken, error)
	Revoke(ctx context.Context, id string) (bool, error)
}

// Recorder는 감사 기록 인터페이스다(*audit.DBRecorder가 만족).
type Recorder interface {
	Record(action string, data map[string]any, actor string)
}

// Deps는 토큰 라우트의 의존성이다.
//
// RequireAdmin/Actor를 함수로 주입받는 이유: httpx가 이 패키지(service_token 저장소)를
// import하므로 이 패키지가 httpx를 import하면 순환이 된다. admin 가드 미들웨어와 principal→actor
// 매핑은 호출자(app.go, httpx/audit import 가능)가 주입한다(consumer-owned 의존성).
type Deps struct {
	Repo         TokenRepo
	Recorder     Recorder
	RequireAdmin func(http.Handler) http.Handler
	Actor        func(ctx context.Context) string
}

// Mount는 토큰 라우트(admin 전용)를 마운트한다.
func Mount(r chi.Router, d Deps) {
	r.Group(func(gr chi.Router) {
		gr.Use(d.RequireAdmin)
		gr.Post("/tokens", d.postToken)
		gr.Get("/tokens", d.listTokens)
		gr.Delete("/tokens/{id}", d.deleteToken)
	})
}

// tokenCreateResponse는 POST /tokens 응답이다. 평문 token은 이 응답에서만 1회 노출.
type tokenCreateResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// tokenListItem은 GET /tokens 항목이다(해시/평문 미노출).
type tokenListItem struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	CreatedAt  jsontime.Time  `json:"createdAt"`
	LastUsedAt *jsontime.Time `json:"lastUsedAt"`
	Revoked    bool           `json:"revoked"`
}

type tokenListResponse struct {
	Tokens []tokenListItem `json:"tokens"`
}

// postToken은 POST /tokens — 발급. 평문 토큰은 이 응답에서만 1회 노출.
func (d Deps) postToken(w http.ResponseWriter, r *http.Request) {
	name := parseTokenName(r.Body)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	plain, hash, err := GenerateToken()
	if err != nil {
		writeInternalError(w)
		return
	}
	row, err := d.Repo.Create(r.Context(), name, hash)
	if err != nil {
		writeInternalError(w)
		return
	}
	d.Recorder.Record("token.create", map[string]any{"id": row.ID, "name": row.Name}, d.Actor(r.Context()))
	writeJSON(w, http.StatusCreated, tokenCreateResponse{ID: row.ID, Name: row.Name, Token: plain})
}

// listTokens는 GET /tokens — 목록(해시/평문 미노출).
func (d Deps) listTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := d.Repo.List(r.Context())
	if err != nil {
		writeInternalError(w)
		return
	}
	items := make([]tokenListItem, 0, len(rows))
	for _, t := range rows {
		item := tokenListItem{
			ID:        t.ID,
			Name:      t.Name,
			CreatedAt: jsontime.New(t.CreatedAt),
			Revoked:   t.RevokedAt != nil,
		}
		if t.LastUsedAt != nil {
			lu := jsontime.New(*t.LastUsedAt)
			item.LastUsedAt = &lu
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, tokenListResponse{Tokens: items})
}

// deleteToken은 DELETE /tokens/:id — 폐기. malformed uuid → 404(§4.4-2).
func (d Deps) deleteToken(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	ok, err := d.Repo.Revoke(r.Context(), id)
	if err != nil {
		writeInternalError(w)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "token not found")
		return
	}
	d.Recorder.Record("token.revoke", map[string]any{"id": id}, d.Actor(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

// parseTokenName은 body에서 trim된 name을 뽑는다. 무효 JSON/비-객체/비-문자열이면 "".
func parseTokenName(body io.Reader) string {
	raw, err := io.ReadAll(body)
	if err != nil {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	name, ok := m["name"].(string)
	if !ok {
		return ""
	}
	return jsutil.TrimJS(name)
}

// ── 응답 헬퍼(httpx 순환 회피용 로컬 복제; JSON 바이트 parity를 위해 SetEscapeHTML(false)) ──

func marshalJS(v any) ([]byte, error) {
	return jsutil.MarshalJSON(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := marshalJS(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeInternalError는 Hono 기본 onError("Internal Server Error", text/plain)를 재현한다.
func writeInternalError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte("Internal Server Error"))
}

// urlParam은 chi 경로 파라미터를 퍼센트 디코딩한다(Hono decodeURIComponent 대응; httpx 순환 회피 로컬).
func urlParam(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if d, err := url.PathUnescape(v); err == nil {
		return d
	}
	return v
}
