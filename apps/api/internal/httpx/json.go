// Package httpx는 chi 라우터 조립, JSON 헬퍼, 미들웨어(요청 로깅·패닉 복구·바디 제한),
// 인증 미들웨어를 제공한다. TS middleware/auth.ts + Hono 부트스트랩을 포팅한다.
package httpx

import (
	"net/http"

	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

// marshalJS는 JS JSON.stringify와 이스케이프 규칙을 맞춘 마샬이다(<>& raw + U+2028/29 raw).
// 단일 원본은 jsutil.MarshalJSON — 응답 바이트 parity의 기본 경로(LFGA-24 §4.3).
func marshalJS(v any) ([]byte, error) {
	return jsutil.MarshalJSON(v)
}

// WriteJSON은 상태코드와 JSON 본문을 쓴다(후행 개행 없음).
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := marshalJS(v)
	if err != nil {
		// 직렬화 불가한 값은 500으로 처리(응답 계약이 깨지지 않게).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// WriteError는 `{"error": msg}` 형태의 오류 응답을 쓴다.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
