package pdp

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Deps는 pdp 모듈의 의존성 묶음이다.
type Deps struct {
	Model    ModelReader
	Policy   PolicyReader
	Gateway  Gateway
	Recorder Recorder
	Auth     httpx.Authenticator
}

// New는 기본 Deps를 만든다(LFGA-27 부트스트랩용).
func New(modelReader ModelReader, policyReader PolicyReader, gateway Gateway, recorder Recorder, auth httpx.Authenticator) Deps {
	return Deps{Model: modelReader, Policy: policyReader, Gateway: gateway, Recorder: recorder, Auth: auth}
}

type handlers struct{ deps Deps }

// Mount는 /access/v1 라우트를 service|admin 가드와 함께 마운트한다.
func Mount(r chi.Router, deps Deps) {
	h := &handlers{deps: deps}
	r.Route("/access/v1", func(r chi.Router) {
		r.Use(httpx.RequireRole(deps.Auth, httpx.RoleService, httpx.RoleAdmin))
		r.Post("/evaluation", h.evaluate)
	})
}

// nestedStr은 m[k1][k2]가 비어있지 않은 문자열이면 (값, true)를 반환한다(TS str(b.x?.y) 대응).
func nestedStr(m map[string]any, k1, k2 string) (string, bool) {
	sub, ok := m[k1].(map[string]any)
	if !ok {
		return "", false
	}
	s, ok := sub[k2].(string)
	return s, ok && len(s) > 0
}

// jsTruthy는 JS truthiness를 재현한다(options.reason 판정용).
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	default:
		return true
	}
}

// POST /access/v1/evaluation (AuthZEN 1.0)
func (h *handlers) evaluate(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var b map[string]any
	if json.Unmarshal(body, &b) != nil {
		b = nil
	}
	subjType, ok1 := nestedStr(b, "subject", "type")
	subjID, ok2 := nestedStr(b, "subject", "id")
	actName, ok3 := nestedStr(b, "action", "name")
	resType, ok4 := nestedStr(b, "resource", "type")
	resID, ok5 := nestedStr(b, "resource", "id")
	allPresent := ok1 && ok2 && ok3 && ok4 && ok5
	if b == nil || !allPresent {
		httpx.WriteError(w, http.StatusBadRequest, "subject{type,id}, action{name}, resource{type,id} are required (non-empty)")
		return
	}

	var reqCtx map[string]any
	contextArray := false
	if c, ok := b["context"].(map[string]any); ok {
		reqCtx = c
	} else if _, isArr := b["context"].([]any); isArr {
		// TS는 typeof []==="object"라 배열도 OpenFGA로 전달하고, OpenFGA가 non-Struct를
		// 거부해 감사+500이 된다. Go SDK 컨텍스트 타입은 map이라 전달 불가 → 동일한 관측
		// 결과(audit pdp.evaluate.openfga_error + 500)를 재현한다(오류 문구는 §4.4-7 마스킹 대상).
		contextArray = true
	}
	reason := false
	if o, ok := b["options"].(map[string]any); ok {
		reason = jsTruthy(o["reason"])
	}

	res, err := evaluate(r.Context(), h.deps, evalInput{
		subjectType:  subjType,
		subjectID:    subjID,
		contextArray: contextArray,
		actionName:   actName,
		resourceType: resType,
		resourceID:   resID,
		context:      reqCtx,
		reason:       reason,
	})
	if err != nil {
		var ee *EvaluateError
		if errors.As(err, &ee) {
			httpx.WriteError(w, http.StatusInternalServerError, "evaluation failed")
			return
		}
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
