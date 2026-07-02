package contract

// named policy 계약(LFGA-8): 정책 1개 = (permission, resourceType) 단일 질문 템플릿.
// description/conditionRef는 없으면 생략한다.
type Policy struct {
	ID           string  `json:"id"`
	Permission   string  `json:"permission"`
	ResourceType string  `json:"resourceType"`
	Description  *string `json:"description,omitempty"`
	ConditionRef *string `json:"conditionRef,omitempty"`
}
