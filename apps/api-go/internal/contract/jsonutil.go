package contract

import (
	"bytes"
	"encoding/json"
)

// marshalNoEscape는 encoding/json의 HTML 이스케이프(`< > &`와 U+2028/U+2029)를 끈 마샬이다.
// TS JSON.stringify는 그 문자들을 이스케이프하지 않으므로, 커스텀 마샬러가 내부에서
// json.Marshal을 쓰면 바이트 parity가 깨진다(LFGA-24 §4.3). 커스텀 마샬러는 전부 이걸 쓴다.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
