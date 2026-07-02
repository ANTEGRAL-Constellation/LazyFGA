package contract

import (
	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

// marshalNoEscape는 JS JSON.stringify와 이스케이프 규칙이 동일한 마샬이다(<>& raw +
// U+2028/29 raw). 커스텀 마샬러가 내부에서 json.Marshal을 쓰면 바이트 parity가 깨지므로
// (LFGA-24 §4.3) 커스텀 마샬러는 전부 이걸 쓴다. 단일 원본은 jsutil.MarshalJSON.
func marshalNoEscape(v any) ([]byte, error) {
	return jsutil.MarshalJSON(v)
}
