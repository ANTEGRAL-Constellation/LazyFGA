package policy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/antegral-constellation/lazyfga/api/internal/audit"
	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps는 policy 모듈의 의존성 묶음이다.
type Deps struct {
	Store    Store
	Model    ModelReader
	Recorder Recorder
	Auth     httpx.Authenticator
}

// Recorder는 감사 기록이다(fire-and-forget).
type Recorder interface {
	Record(action string, data map[string]any, actor string)
}

// New는 pgx 풀 기반 기본 Deps를 만든다(LFGA-27 부트스트랩용).
func New(pool *pgxpool.Pool, modelReader ModelReader, recorder Recorder, auth httpx.Authenticator) Deps {
	return Deps{Store: NewRepo(pool), Model: modelReader, Recorder: recorder, Auth: auth}
}

type handlers struct{ deps Deps }

// Mount는 /policies 라우트를 admin 가드와 함께 마운트한다.
func Mount(r chi.Router, deps Deps) {
	h := &handlers{deps: deps}
	r.Route("/policies", func(r chi.Router) {
		r.Use(httpx.RequireRole(deps.Auth, httpx.RoleAdmin))
		r.Use(httpx.TrailingSlash404)
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Get("/{id}", h.get)
		r.Put("/{id}", h.update)
		r.Delete("/{id}", h.del)
	})
}

type policyEnvelope struct {
	Policy *contract.Policy `json:"policy"`
}
type policiesEnvelope struct {
	Policies []contract.Policy `json:"policies"`
}

func (h *handlers) actor(r *http.Request) string {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return audit.PrincipalActor(p)
}

func stringField(m map[string]any, key string) *string {
	if s, ok := m[key].(string); ok {
		return &s
	}
	return nil
}

// POST /policies
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		m = nil
	}
	id, ok1 := m["id"].(string)
	perm, ok2 := m["permission"].(string)
	rtype, ok3 := m["resourceType"].(string)
	if m == nil || !ok1 || !ok2 || !ok3 {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "id, permission, resourceType are required")
		return
	}
	p, err := createPolicy(r.Context(), h.deps, createInput{
		ID:           id,
		Permission:   perm,
		ResourceType: rtype,
		Description:  stringField(m, "description"),
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.deps.Recorder.Record("policy.create", map[string]any{"id": p.ID}, h.actor(r))
	httpx.WriteJSON(w, http.StatusCreated, policyEnvelope{Policy: p})
}

// GET /policies
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	policies, err := h.deps.Store.ListPolicies(r.Context())
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, policiesEnvelope{Policies: policies})
}

// GET /policies/:id
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	p, err := h.deps.Store.FindByID(r.Context(), httpx.URLParam(r, "id"))
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if p == nil {
		httpx.WriteError(w, http.StatusNotFound, "policy not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, policyEnvelope{Policy: p})
}

// PUT /policies/:id
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	id := httpx.URLParam(r, "id")
	existing, err := h.deps.Store.FindByID(r.Context(), id)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if existing == nil {
		httpx.WriteError(w, http.StatusNotFound, "policy not found")
		return
	}
	body, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(body, &m) // 무효 → {} 등가(m nil → 모든 필드 부재).
	updated, err := editPolicy(r.Context(), h.deps, id, patchInput{
		Permission:   stringField(m, "permission"),
		ResourceType: stringField(m, "resourceType"),
		Description:  stringField(m, "description"),
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.deps.Recorder.Record("policy.update", map[string]any{"id": id}, h.actor(r))
	httpx.WriteJSON(w, http.StatusOK, policyEnvelope{Policy: updated})
}

// DELETE /policies/:id
func (h *handlers) del(w http.ResponseWriter, r *http.Request) {
	id := httpx.URLParam(r, "id")
	ok, err := h.deps.Store.DeletePolicy(r.Context(), id)
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "policy not found")
		return
	}
	h.deps.Recorder.Record("policy.delete", map[string]any{"id": id}, h.actor(r))
	w.WriteHeader(http.StatusNoContent)
}

// writeErr은 PolicyError는 {"error": detail} + 상태로, 그 외는 Hono 기본 500으로 낸다.
func (h *handlers) writeErr(w http.ResponseWriter, err error) {
	var pe *PolicyError
	if errors.As(err, &pe) {
		httpx.WriteError(w, pe.Status, pe.Detail)
		return
	}
	httpx.WriteHonoInternalError(w)
}
