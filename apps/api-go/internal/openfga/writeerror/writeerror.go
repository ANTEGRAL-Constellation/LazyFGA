// Package writeerror는 OpenFGA write/delete 오류를 분류한다(TS write-error.ts 포팅).
// idp(웹훅 동기화)·permission(grant/revoke)이 공유하므로 분류 의미는 단일 소스로 둔다.
package writeerror

import (
	"errors"
	"net"
	"strings"

	fga "github.com/openfga/go-sdk"
)

// Op는 멱등 흡수 패턴을 op별로 구분하기 위한 연산 종류다.
type Op string

const (
	OpWrite  Op = "write"
	OpDelete Op = "delete"
)

// Classification은 결정적/일시적/멱등 판정 결과다.
type Classification struct {
	Idempotent bool
	Transient  bool
}

// invalidInputCode는 OpenFGA가 중복 write / 없는 delete에 대해 내는 코드다.
const invalidInputCode = "write_failed_due_to_invalid_input"

// responseStatusCoder는 SDK Fga*Error가 공통으로 구현하는 상태코드 접근자다.
type responseStatusCoder interface {
	ResponseStatusCode() int
}

// IsTransientAPIError는 재시도 가능한(일시적) OpenFGA/SDK 오류인지 보고한다.
//   - HTTP status >=500 또는 429 → transient.
//   - status가 없음(네트워크 단계): SDK 네트워크 오류(*url.Error/net.Error), 인식되는
//     네트워크 오류 클래스, 또는 parity 메시지 패턴이면 transient.
//   - 그 외 status 없는 오류는 결정적(정체불명 + status 없음 → 무한재시도 방지, false).
//   - 결정적 4xx는 메시지 내용과 무관하게 절대 transient가 아니다.
func IsTransientAPIError(e error) bool {
	if e == nil {
		return false
	}
	// FgaApiInternalError(5xx)·RateLimit(429)은 항상 상태코드가 규칙을 만족하지만
	// 명시적으로도 잡아 TS의 instanceof 분기와 1:1 대응시킨다.
	var internalErr fga.FgaApiInternalError
	if errors.As(e, &internalErr) {
		return true
	}
	var rateErr fga.FgaApiRateLimitExceededError
	if errors.As(e, &rateErr) {
		return true
	}

	// HTTP status가 있는 오류: 5xx/429만 transient, 4xx는 결정적.
	var coder responseStatusCoder
	if errors.As(e, &coder) {
		code := coder.ResponseStatusCode()
		return code >= 500 || code == 429
	}

	// 여기부터는 HTTP status가 없는 오류.
	if isNetworkError(e) {
		return true
	}
	return matchesTransientMessage(e)
}

// ClassifyWriteError는 write/delete 오류를 {Idempotent, Transient}로 분류한다.
// transient면 그대로, 아니면 invalid-input 신호 + op별 정확 패턴이 함께 맞을 때만 멱등 흡수한다.
//   - write 멱등 = 중복 tuple: "already exists" | "duplicate".
//   - delete 멱등 = 없는 tuple: "cannot delete" | "does not exist".
//
// delete에서 막연한 "not found"는 제외한다 — 실제 거부(type/relation not found)를
// missing no-op으로 삼켜 숨기는 over-match를 막는다(LFGA-20 review).
func ClassifyWriteError(e error, op Op) Classification {
	if IsTransientAPIError(e) {
		return Classification{Idempotent: false, Transient: true}
	}
	msg := strings.ToLower(errMessage(e))
	isInvalidInput := responseCode(e) == invalidInputCode || strings.Contains(msg, invalidInputCode)

	var opPattern bool
	switch op {
	case OpWrite:
		opPattern = strings.Contains(msg, "already exists") || strings.Contains(msg, "duplicate")
	case OpDelete:
		opPattern = strings.Contains(msg, "cannot delete") || strings.Contains(msg, "does not exist")
	}
	return Classification{Idempotent: isInvalidInput && opPattern, Transient: false}
}

// responseCode는 검증 오류(FgaApiValidationError)의 API 코드 문자열을 반환한다.
// TS의 `e.responseData?.code` 경로에 대응한다.
func responseCode(e error) string {
	var v fga.FgaApiValidationError
	if errors.As(e, &v) {
		return string(v.ResponseCode())
	}
	return ""
}

// errMessage는 오류 메시지를 안전하게 추출한다.
func errMessage(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// isNetworkError는 네트워크 단계 오류를 판별한다. net.Error는 *url.Error·*net.OpError·
// *net.DNSError·syscall.Errno를 모두 포함한다(전부 Timeout()/Temporary()를 구현). SDK는
// 연결 실패(연결 거부/리셋/타임아웃/DNS)를 *url.Error로 감싸므로 net.Error 하나로 잡힌다.
// TS의 "FgaApiError(status 부재) + TRANSIENT_CODES → transient"에 대응한다.
func isNetworkError(e error) bool {
	var netErr net.Error
	return errors.As(e, &netErr)
}

// matchesTransientMessage는 TS parity 메시지 패턴 폴백이다.
func matchesTransientMessage(e error) bool {
	m := strings.ToLower(errMessage(e))
	return strings.Contains(m, "fetch failed") ||
		strings.Contains(m, "network") ||
		strings.Contains(m, "timeout") ||
		strings.Contains(m, "econnrefused")
}
