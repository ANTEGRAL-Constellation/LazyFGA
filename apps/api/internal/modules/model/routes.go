package model

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/jsontime"
	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps는 model 모듈의 의존성 묶음이다.
type Deps struct {
	Store    Store
	Gateway  Gateway
	Compiler Compiler
	Recorder Recorder
	Auth     httpx.Authenticator
}

// New는 pgx 풀 기반 기본 Deps를 만든다(LFGA-27 부트스트랩용).
func New(pool *pgxpool.Pool, gateway Gateway, recorder Recorder, auth httpx.Authenticator) Deps {
	return Deps{
		Store:    NewRepo(pool),
		Gateway:  gateway,
		Compiler: DefaultCompiler(),
		Recorder: recorder,
		Auth:     auth,
	}
}

type handlers struct{ deps Deps }

// Mount는 /model 라우트를 admin 가드와 함께 마운트한다.
func Mount(r chi.Router, deps Deps) {
	h := &handlers{deps: deps}
	r.Route("/model", func(r chi.Router) {
		r.Use(httpx.RequireRole(deps.Auth, httpx.RoleAdmin))
		r.Use(httpx.TrailingSlash404)
		r.Post("/", h.publish)
		r.Get("/current", h.current)
		r.Get("/versions", h.listVersions)
		r.Get("/diff", h.diff) // :id보다 먼저 등록(별도 리터럴 세그먼트라 충돌 없음).
		r.Get("/versions/{id}", h.getVersion)
	})
}

// 응답 DTO(TS 라우트 리터럴과 필드/순서 동일).
type versionDTO struct {
	ID                   string        `json:"id"`
	AuthorizationModelID string        `json:"authorizationModelId"`
	CreatedAt            jsontime.Time `json:"createdAt"`
	Note                 *string       `json:"note"` // null 유지(omitempty 아님).
}

type versionEnvelope struct {
	Version versionDTO      `json:"version"`
	IR      json.RawMessage `json:"ir"`
	DSL     string          `json:"dsl"`
}

type versionsList struct {
	Versions []versionDTO `json:"versions"`
}

type publishedVersionDTO struct {
	ID                   string        `json:"id"`
	AuthorizationModelID string        `json:"authorizationModelId"`
	CreatedAt            jsontime.Time `json:"createdAt"`
}

type publishedEnvelope struct {
	Version publishedVersionDTO `json:"version"`
}

type shapeError struct {
	Error  string           `json:"error"`
	Issues []contract.Issue `json:"issues"`
}

type publishErrorBody struct {
	Error  string `json:"error"`
	Detail any    `json:"detail"`
}

type diffResult struct {
	Changes []DiffChange `json:"changes"`
}

func toVersionDTO(v *Version) versionDTO {
	return versionDTO{
		ID:                   v.ID,
		AuthorizationModelID: v.AuthorizationModelID,
		CreatedAt:            jsontime.New(v.CreatedAt),
		Note:                 v.Note,
	}
}

// createdByOf는 principal을 발행 actor("admin" | "token:<id>")로 매핑한다(TS와 동일).
func createdByOf(p httpx.Principal) string {
	if p.Role == httpx.RoleAdmin {
		return "admin"
	}
	id := p.TokenID
	if id == "" {
		id = "?"
	}
	return "token:" + id
}

// POST /model — IR 발행.
func (h *handlers) publish(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var env struct {
		IR   json.RawMessage `json:"ir"`
		Note json.RawMessage `json:"note"`
	}
	_ = json.Unmarshal(body, &env) // 무효/부재 → env.IR nil → 아래 shape 검증에서 422.

	ir, issues := contract.DecodeModelIR(env.IR)
	if len(issues) > 0 {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, shapeError{Error: "invalid IR shape", Issues: issues})
		return
	}

	var note *string
	if len(env.Note) > 0 && env.Note[0] == '"' { // typeof body.note === "string"만 채택.
		var s string
		if json.Unmarshal(env.Note, &s) == nil {
			note = &s
		}
	}

	p, _ := httpx.PrincipalFromContext(r.Context())
	createdBy := createdByOf(p)

	// TS는 zod parsed.data(미지의 키 제거 + 숫자 정규화)를 저장한다 — 요청 raw가 아니라
	// 디코드된 IR의 canonical 직렬화를 저장해 동일 의미를 재현한다(LFGA-26 리뷰 반영).
	canonicalIR, err := jsutil.MarshalJSON(ir)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	pv, err := publishModel(r.Context(), h.deps, ir, canonicalIR, note, createdBy)
	if err != nil {
		var pe *PublishError
		if errors.As(err, &pe) {
			httpx.WriteJSON(w, pe.Status, publishErrorBody{Error: pe.Error(), Detail: pe.Detail})
			return
		}
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, publishedEnvelope{Version: publishedVersionDTO{
		ID:                   pv.ID,
		AuthorizationModelID: pv.AuthorizationModelID,
		CreatedAt:            jsontime.New(pv.CreatedAt),
	}})
}

// GET /model/current
func (h *handlers) current(w http.ResponseWriter, r *http.Request) {
	v, err := h.deps.Store.CurrentVersion(r.Context())
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if v == nil {
		httpx.WriteError(w, http.StatusNotFound, "no model published yet")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, versionEnvelope{Version: toVersionDTO(v), IR: v.IRJSON, DSL: v.DSL})
}

// GET /model/versions
func (h *handlers) listVersions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.deps.Store.ListVersions(r.Context())
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	dtos := make([]versionDTO, 0, len(rows))
	for i := range rows {
		dtos = append(dtos, toVersionDTO(&rows[i]))
	}
	httpx.WriteJSON(w, http.StatusOK, versionsList{Versions: dtos})
}

// GET /model/diff?from=&to=
func (h *handlers) diff(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		httpx.WriteError(w, http.StatusBadRequest, "from and to query params required")
		return
	}
	a, err := h.deps.Store.GetVersion(r.Context(), from)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	b, err := h.deps.Store.GetVersion(r.Context(), to)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if a == nil || b == nil {
		httpx.WriteError(w, http.StatusNotFound, "version not found")
		return
	}
	airr, err := a.IR()
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	birr, err := b.IR()
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, diffResult{Changes: DiffModels(airr, birr)})
}

// GET /model/versions/:id
func (h *handlers) getVersion(w http.ResponseWriter, r *http.Request) {
	v, err := h.deps.Store.GetVersion(r.Context(), httpx.URLParam(r, "id"))
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if v == nil {
		httpx.WriteError(w, http.StatusNotFound, "version not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, versionEnvelope{Version: toVersionDTO(v), IR: v.IRJSON, DSL: v.DSL})
}
