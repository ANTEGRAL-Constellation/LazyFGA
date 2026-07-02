package pdp

import (
	"context"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/modules/model"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
)

// EvaluateError는 OpenFGA 오류(모델 깨짐 등)를 500으로 표면화한다(fail-closed가 아닌 무결성 이슈).
type EvaluateError struct{ detail string }

func (e *EvaluateError) Error() string { return "evaluate failed" }

// ModelReader는 현재 발행 모델을 읽는다(consumer-owned).
type ModelReader interface {
	CurrentVersion(ctx context.Context) (*model.Version, error)
}

// PolicyReader는 (action, resource.type)로 정책을 찾는다(consumer-owned).
type PolicyReader interface {
	FindByActionResource(ctx context.Context, permission, resourceType string) (*contract.Policy, error)
}

// Recorder는 감사 기록이다(fire-and-forget).
type Recorder interface {
	Record(action string, data map[string]any, actor string)
}

// evalInput은 라우트가 파싱해 evaluate에 넘기는 정규화 입력이다.
type evalInput struct {
	subjectType  string
	subjectID    string
	actionName   string
	resourceType string
	resourceID   string
	context      map[string]any
	// contextArray는 context가 JSON 배열이었음을 뜻한다. TS는 배열도 OpenFGA로 전달하고
	// OpenFGA가 non-Struct를 거부해 감사+500이 되므로, Check 시점에 같은 결과를 재현한다.
	contextArray bool
	reason       bool
}

// evaluate는 단일 질문 템플릿 평가(TS pdp.evaluator.evaluate)를 수행한다.
// 정책/모델 부재 → deny-by-default(200). OpenFGA 자체 오류 → EvaluateError(500).
// reason 재구성 실패는 decision을 무효화하지 않는다(swallow + audit).
func evaluate(ctx context.Context, deps Deps, req evalInput) (contract.EvaluationResponse, error) {
	current, err := deps.Model.CurrentVersion(ctx)
	if err != nil {
		return contract.EvaluationResponse{}, err // raw → Hono 기본 500.
	}
	if current == nil {
		return contract.EvaluationResponse{Decision: false, Context: map[string]any{"reason_code": "MODEL_NOT_PUBLISHED"}}, nil
	}

	pol, err := deps.Policy.FindByActionResource(ctx, req.actionName, req.resourceType)
	if err != nil {
		return contract.EvaluationResponse{}, err // raw → Hono 기본 500.
	}
	if pol == nil {
		return contract.EvaluationResponse{Decision: false, Context: map[string]any{"reason_code": "NO_POLICY"}}, nil
	}

	user := req.subjectType + ":" + req.subjectID
	object := req.resourceType + ":" + req.resourceID
	relation := "can_" + pol.Permission

	if req.contextArray {
		// TS 관측 결과 재현: OpenFGA가 배열 context를 거부 → 감사 + 500(§4.4 오류 문구 마스킹 대상).
		msg := "context must be a JSON object (array given)"
		deps.Recorder.Record("pdp.evaluate.openfga_error", map[string]any{"user": user, "relation": relation, "object": object, "error": msg}, "")
		return contract.EvaluationResponse{}, &EvaluateError{detail: msg}
	}

	allowed, err := deps.Gateway.Check(ctx,
		openfga.CheckInput{User: user, Relation: relation, Object: object, Context: req.context},
		openfga.WithCheckAuthorizationModelID(current.AuthorizationModelID))
	if err != nil {
		deps.Recorder.Record("pdp.evaluate.openfga_error", map[string]any{"user": user, "relation": relation, "object": object, "error": err.Error()}, "")
		return contract.EvaluationResponse{}, &EvaluateError{detail: err.Error()}
	}

	if !req.reason {
		return contract.EvaluationResponse{Decision: allowed}, nil
	}

	// reason은 요청 시에만(hot-path 경량 유지). 실패는 decision을 무효화하지 않는다.
	ir, err := current.IR()
	if err != nil {
		deps.Recorder.Record("pdp.reason.error", map[string]any{"user": user, "relation": relation, "object": object, "error": err.Error()}, "")
		return contract.EvaluationResponse{Decision: allowed}, nil
	}
	reason, err := explain(ctx, deps.Gateway,
		reasonPin{decision: allowed, authorizationModelID: current.AuthorizationModelID, ir: ir},
		user, pol.Permission, object, req.context)
	if err != nil {
		deps.Recorder.Record("pdp.reason.error", map[string]any{"user": user, "relation": relation, "object": object, "error": err.Error()}, "")
		return contract.EvaluationResponse{Decision: allowed}, nil
	}
	return contract.EvaluationResponse{Decision: allowed, Context: map[string]any{"reason": reason}}, nil
}
