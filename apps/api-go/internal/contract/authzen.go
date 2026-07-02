package contract

// OpenID AuthZEN 1.0 Access Evaluation 요청/응답(LFGA-9).

type EvalSubject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type EvalAction struct {
	Name string `json:"name"`
}

type EvalResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// EvalOptions: reason=true면 응답 context.reason에 설명을 부착(LFGA-11).
type EvalOptions struct {
	Reason *bool `json:"reason,omitempty"`
}

type EvaluationRequest struct {
	Subject  EvalSubject    `json:"subject"`
	Action   EvalAction     `json:"action"`
	Resource EvalResource   `json:"resource"`
	Context  map[string]any `json:"context,omitempty"`
	Options  *EvalOptions   `json:"options,omitempty"`
}

type EvaluationResponse struct {
	Decision bool           `json:"decision"`
	Context  map[string]any `json:"context,omitempty"`
}
