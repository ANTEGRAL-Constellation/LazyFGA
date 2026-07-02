// Package jsutil은 JS(ECMA-262) 문자열/숫자 직렬화를 바이트 단위로 재현하는 헬퍼를
// 제공한다. TS 컴파일러(condition-to-cel)는 `JSON.stringify`와 `String(number)`로 CEL
// 리터럴을 만들기 때문에, Go 포트가 동일한 CEL을 emit하려면 그 규칙을 정확히 맞춰야 한다.
// (LFGA-24 §4.3 — idp extraction 엔진[LFGA-26]도 이 헬퍼를 공유해 포맷 parity를 한 곳에 둔다.)
package jsutil

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// MarshalJSON은 JS `JSON.stringify`와 이스케이프 규칙을 맞춘 마샬이다:
//   - `< > &`를 이스케이프하지 않는다(SetEscapeHTML(false)).
//   - U+2028/U+2029를 raw로 출력한다 — Go encoding/json은 SetEscapeHTML(false)여도
//     이 둘은 항상  / 로 이스케이프하므로 후처리로 되돌린다.
//
// 커스텀 MarshalJSON 구현과 응답 직렬화 경로(httpx)가 전부 이 헬퍼를 공유해
// 바이트 parity의 단일 원본이 된다(LFGA-24 §4.3).
func MarshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	return unescapeLineSeps(b), nil
}

// unescapeLineSeps는 JSON 텍스트 안의  /  이스케이프를 raw 문자로 되돌린다.
// 인코더가 만든 이스케이프만 바꾸도록 백슬래시 run의 짝수/홀수(=이스케이프 시작 여부)를
// 추적한다 — 원문에 있던 리터럴 `\\u2028`(이스케이프된 백슬래시 + 텍스트)은 건드리지 않는다.
func unescapeLineSeps(b []byte) []byte {
	if !bytes.Contains(b, []byte(`\u202`)) {
		return b
	}
	var out bytes.Buffer
	out.Grow(len(b))
	for i := 0; i < len(b); {
		c := b[i]
		if c != '\\' {
			out.WriteByte(c)
			i++
			continue
		}
		// 백슬래시 run 시작: 홀수 번째 백슬래시 뒤의 u2028/u2029만 인코더 산출 이스케이프다.
		run := 0
		for i+run < len(b) && b[i+run] == '\\' {
			run++
		}
		pairs := run / 2
		for j := 0; j < pairs*2; j++ {
			out.WriteByte('\\')
		}
		i += pairs * 2
		if run%2 == 1 {
			rest := b[i:]
			switch {
			case bytes.HasPrefix(rest, []byte(`\u2028`)):
				out.WriteString(" ")
				i += 6
			case bytes.HasPrefix(rest, []byte(`\u2029`)):
				out.WriteString(" ")
				i += 6
			default:
				// 그 외 이스케이프(\n, \", \u00XX 등)는 그대로 통과.
				out.WriteByte('\\')
				i++
			}
		}
	}
	return out.Bytes()
}

// TrimJS는 JS `String.prototype.trim`과 동일한 문자 집합(ES WhiteSpace ∪ LineTerminator)을
// 양끝에서 제거한다. Go strings.TrimSpace와의 차이: U+FEFF(ZWNBSP)를 제거하고,
// U+0085(NEL)는 제거하지 않는다.
func TrimJS(s string) string {
	return strings.TrimFunc(s, isJSWhitespace)
}

func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\v', '\f', ' ', '\n', '\r', 0x00a0, 0xfeff, 0x2028, 0x2029:
		return true
	}
	// Unicode Zs(공백 분리자) 카테고리.
	return r == 0x1680 || (r >= 0x2000 && r <= 0x200a) || r == 0x202f || r == 0x205f || r == 0x3000
}

// JSONString은 JS `JSON.stringify(s)`(문자열 인자)와 바이트 단위로 동일한 큰따옴표 감싼
// 리터럴을 반환한다. Go 표준 encoding/json과 다른 점:
//   - `< > &`를 이스케이프하지 않는다(SetEscapeHTML(false) 의미).
//   - U+2028/U+2029(라인/문단 구분자)를 raw로 출력한다(Go는 항상  / 로 이스케이프).
//
// ECMA-262 QuoteJSONString: 제어문자(<0x20)는 축약(\b\t\n\f\r) 또는 \u00XX(소문자 hex),
// `"`·`\`는 이스케이프, 그 외(멀티바이트 유니코드 포함)는 raw로 출력한다.
func JSONString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hex[(r>>4)&0xf])
				b.WriteByte(hex[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// NumberString은 JS `String(f)`/`Number::toString(f)`와 바이트 단위로 동일한 십진 표현을
// 반환한다. 핵심 차이:
//   - -0 → "0".
//   - 크기가 [1e-6, 1e21) 이면 십진 표기, 그 밖은 지수 표기.
//   - 지수는 JS 형식(`1e-7`, `1e+21`) — Go strconv의 `1e-07`처럼 0으로 패딩하지 않는다.
//   - 최단 왕복(shortest round-trip) 표현.
func NumberString(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if f == 0 { // +0 과 -0 모두 "0".
		return "0"
	}
	if f < 0 {
		return "-" + NumberString(-f)
	}

	// 최단 지수 표기에서 유효숫자(digits)와 지수 E를 얻는다: value = d.ddd × 10^E.
	// mantissa의 소수점을 제거한 digits 문자열 s(길이 k)와 n = E+1 로 ECMA-262 규칙을 적용한다
	// (value = s × 10^(n-k)).
	es := strconv.FormatFloat(f, 'e', -1, 64) // 예: "1e-07", "1.2345e+02"
	ei := strings.IndexByte(es, 'e')
	mantissa := es[:ei]
	exp, _ := strconv.Atoi(es[ei+1:])
	digits := strings.Replace(mantissa, ".", "", 1)
	k := len(digits)
	n := exp + 1

	switch {
	case k <= n && n <= 21:
		// 정수부만: 유효숫자 뒤에 (n-k)개의 0.
		return digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		// 소수점이 유효숫자 중간에: 앞 n자리 . 나머지.
		return digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		// "0." + (-n)개의 0 + 유효숫자.
		return "0." + strings.Repeat("0", -n) + digits
	default:
		// 지수 표기.
		var b strings.Builder
		b.WriteByte(digits[0])
		if k > 1 {
			b.WriteByte('.')
			b.WriteString(digits[1:])
		}
		b.WriteByte('e')
		e := n - 1
		if e >= 0 {
			b.WriteByte('+')
			b.WriteString(strconv.Itoa(e))
		} else {
			b.WriteByte('-')
			b.WriteString(strconv.Itoa(-e))
		}
		return b.String()
	}
}
