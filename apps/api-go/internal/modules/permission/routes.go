package permission

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/antegral-constellation/lazyfga/api/internal/audit"
	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Deps는 permission 모듈의 의존성 묶음이다.
type Deps struct {
	Model    ModelReader
	Gateway  Gateway
	Recorder Recorder
	Auth     httpx.Authenticator
}

// New는 기본 Deps를 만든다(LFGA-27 부트스트랩용).
func New(modelReader ModelReader, gateway Gateway, recorder Recorder, auth httpx.Authenticator) Deps {
	return Deps{Model: modelReader, Gateway: gateway, Recorder: recorder, Auth: auth}
}

type handlers struct{ deps Deps }

// Mount는 /grants 라우트를 admin 가드와 함께 마운트한다.
func Mount(r chi.Router, deps Deps) {
	h := &handlers{deps: deps}
	r.Route("/grants", func(r chi.Router) {
		r.Use(httpx.RequireRole(deps.Auth, httpx.RoleAdmin))
		r.Post("/", h.grant)
		r.Delete("/", h.revoke)
		r.Get("/", h.list)
	})
}

// resourceTypeRE는 GET ?resourceType 필터 검증 규칙이다.
var resourceTypeRE = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

type codeError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}
type grantedBody struct {
	Granted bool `json:"granted"`
	Created bool `json:"created"`
}
type revokedBody struct {
	Revoked bool `json:"revoked"`
	Deleted bool `json:"deleted"`
}
type grantsBody struct {
	Grants []contract.GrantEntry `json:"grants"`
}

func (h *handlers) actor(r *http.Request) string {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return audit.PrincipalActor(p)
}

// onError는 GrantError는 {"error": detail, "code": code} + 상태로, 그 외는 Hono 기본 500으로 낸다.
func (h *handlers) onError(w http.ResponseWriter, err error) {
	var ge *GrantError
	if errors.As(err, &ge) {
		httpx.WriteJSON(w, ge.Status, codeError{Error: ge.Detail, Code: ge.Code})
		return
	}
	httpx.WriteHonoInternalError(w)
}

// POST /grants — 단일 배정 tuple write. 201(신규) | 200(이미 존재, no-op).
func (h *handlers) grant(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req, ok := decodeGrantRequest(body)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, codeError{Error: "malformed grant request", Code: "malformed_request"})
		return
	}
	created, err := grant(r.Context(), h.deps, req, h.actor(r))
	if err != nil {
		h.onError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, grantedBody{Granted: true, Created: created})
}

// DELETE /grants — 단일 배정 tuple delete. 200(삭제 | 이미 없음, no-op).
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req, ok := decodeRevokeRequest(body)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, codeError{Error: "malformed revoke request", Code: "malformed_request"})
		return
	}
	deleted, err := revoke(r.Context(), h.deps, req, h.actor(r))
	if err != nil {
		h.onError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, revokedBody{Revoked: true, Deleted: deleted})
}

// GET /grants?resource=<type>:<id> | ?subject=<type>:<id>[#<relation>][&resourceType=<t>]
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resource := q.Get("resource")
	subject := q.Get("subject")
	if (resource != "" && subject != "") || (resource == "" && subject == "") {
		httpx.WriteJSON(w, http.StatusBadRequest, codeError{Error: "supply exactly one of `resource` or `subject`", Code: "malformed_request"})
		return
	}

	if resource != "" {
		ref, ok := contract.ParseResourceRef(resource)
		if !ok {
			httpx.WriteJSON(w, http.StatusBadRequest, codeError{Error: `invalid resource "` + resource + `"`, Code: "malformed_request"})
			return
		}
		entries, err := listByResource(r.Context(), h.deps, ref)
		if err != nil {
			h.onError(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, grantsBody{Grants: entries})
		return
	}

	subj, ok := contract.ParseGrantSubject(subject)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, codeError{Error: `invalid subject "` + subject + `"`, Code: "malformed_request"})
		return
	}
	var resourceType *string
	if vals, present := q["resourceType"]; present {
		v := vals[0]
		if !resourceTypeRE.MatchString(v) {
			httpx.WriteJSON(w, http.StatusBadRequest, codeError{Error: `invalid resourceType "` + v + `"`, Code: "malformed_request"})
			return
		}
		resourceType = &v
	}
	entries, err := listBySubject(r.Context(), h.deps, subj, resourceType)
	if err != nil {
		h.onError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, grantsBody{Grants: entries})
}

// decodeGrantRequest는 신뢰 못 할 본문을 GrantRequest 형태로 strict 디코딩한다(zod grantRequestSchema 등가).
// z.object는 non-strict라 잉여 키는 무시하고, 필수 필드 부재/타입불일치는 실패(→ 400)다.
func decodeGrantRequest(data []byte) (*contract.GrantRequest, bool) {
	m, ok := asObject(data)
	if !ok {
		return nil, false
	}
	req, ok := decodeCommon(m)
	if !ok {
		return nil, false
	}
	if cv, present := m["condition"]; present {
		cm, ok := cv.(map[string]any)
		if !ok {
			return nil, false
		}
		cname, ok := cm["name"].(string)
		if !ok {
			return nil, false
		}
		cond := &contract.GrantCondition{Name: cname}
		if ctxv, present := cm["context"]; present {
			ctxM, ok := ctxv.(map[string]any)
			if !ok {
				return nil, false
			}
			cond.Context = ctxM
		}
		req.Condition = cond
	}
	return req, true
}

// decodeRevokeRequest는 조건 없는 GrantRequest 형태를 디코딩한다.
func decodeRevokeRequest(data []byte) (*contract.RevokeRequest, bool) {
	m, ok := asObject(data)
	if !ok {
		return nil, false
	}
	req, ok := decodeCommon(m)
	if !ok {
		return nil, false
	}
	return &contract.RevokeRequest{Subject: req.Subject, Relation: req.Relation, Resource: req.Resource}, true
}

// decodeCommon은 subject/relation/resource 공통 형태를 디코딩한다.
func decodeCommon(m map[string]any) (*contract.GrantRequest, bool) {
	subM, ok := m["subject"].(map[string]any)
	if !ok {
		return nil, false
	}
	styp, ok := subM["type"].(string)
	if !ok {
		return nil, false
	}
	sid, ok := subM["id"].(string)
	if !ok {
		return nil, false
	}
	subj := contract.GrantSubject{Type: styp, ID: sid}
	if rv, present := subM["relation"]; present {
		rs, ok := rv.(string)
		if !ok {
			return nil, false
		}
		subj.Relation = &rs
	}
	rel, ok := m["relation"].(string)
	if !ok {
		return nil, false
	}
	resM, ok := m["resource"].(map[string]any)
	if !ok {
		return nil, false
	}
	rtyp, ok := resM["type"].(string)
	if !ok {
		return nil, false
	}
	rid, ok := resM["id"].(string)
	if !ok {
		return nil, false
	}
	return &contract.GrantRequest{Subject: subj, Relation: rel, Resource: contract.ResourceRef{Type: rtyp, ID: rid}}, true
}

func asObject(data []byte) (map[string]any, bool) {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}
