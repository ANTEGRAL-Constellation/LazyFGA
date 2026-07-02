package contract

// Issue: strict shape 디코딩 위반(zod 등가). LFGA-25가 422 {"error":"invalid IR shape",issues}
// 로 매핑한다. zod 내부 이슈 객체와 달리 {path, message}만 담는다(승인된 편차 LFGA-22 §4.4-1).
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError: 의미 검증 위반(ValidateModelIR). code/path는 TS와 바이트 동일.
type ValidationError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ConditionError: 조건 정의 정적 검증 위반. ValidateModelIR가 ValidationError로 승격한다.
type ConditionError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}
