package jsutil

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

// TestJSONString은 JS `JSON.stringify` 문자열 규칙 중 리터럴로 안전히 표기 가능한 케이스를
// 검증한다(제어문자/구분자 raw는 TestJSONStringSpecial에서 프로그램적으로 구성).
func TestJSONString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", `""`},
		{"plain", "gold", `"gold"`},
		{"html-chars-raw", "a<b>c&d", `"a<b>c&d"`}, // <>& 는 raw.
		{"quote", `q"b`, `"q\"b"`},
		{"backslash", `back\slash`, `"back\\slash"`},
		{"newline", "line\nfeed", `"line\nfeed"`},
		{"carriage", "carriage\rreturn", `"carriage\rreturn"`},
		{"tab", "tab\there", `"tab\there"`},
		{"formfeed", "form\ffeed", `"form\ffeed"`},
		{"backspace", "back\bspace", `"back\bspace"`},
		{"slash-raw", "slash/here", `"slash/here"`}, // / 는 이스케이프하지 않음.
		{"unicode-raw", "unicodeé😀", `"unicodeé😀"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := JSONString(tc.in); got != tc.want {
				t.Fatalf("JSONString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestJSONStringSpecial은 제어문자(\u00XX)와 raw로 남아야 하는 특수문자(DEL, U+2028/U+2029)를
// 리터럴 없이 rune/fmt로 구성해 검증한다. Go encoding/json과 갈리는 지점이다.
func TestJSONStringSpecial(t *testing.T) {
	esc := func(codepoints ...rune) string { // \u00XX 이스케이프된 기대 본문.
		out := ""
		for _, r := range codepoints {
			out += fmt.Sprintf(`\u%04x`, r)
		}
		return `"` + out + `"`
	}
	raw := func(codepoints ...rune) string { // raw 유지 기대(quote 감쌈).
		out := ""
		for _, r := range codepoints {
			out += string(r)
		}
		return `"` + out + `"`
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"nul", string(rune(0x00)), esc(0x00)},
		{"control-low", string(rune(0x01)) + string(rune(0x1f)), esc(0x01, 0x1f)},
		{"escape-char", string(rune(0x1b)), esc(0x1b)},
		{"del-raw", string(rune(0x7f)), raw(0x7f)},                           // 0x7f 는 raw.
		{"c1-raw", string(rune(0x80)) + string(rune(0x9f)), raw(0x80, 0x9f)}, // C1 은 raw.
		{"line-separator-raw", string(rune(0x2028)), raw(0x2028)},
		{"para-separator-raw", string(rune(0x2029)), raw(0x2029)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := JSONString(tc.in); got != tc.want {
				t.Fatalf("JSONString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNumberString은 JS `Number::toString` 재현을 검증한다(oracle: Node).
func TestNumberString(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{math.Copysign(0, -1), "0"}, // -0 → "0"
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{-42, "-42"},
		{100, "100"},
		{123.45, "123.45"},
		{3.14, "3.14"},
		{0.5, "0.5"},
		{1000000, "1000000"},
		{999999, "999999"},
		{0.0001, "0.0001"},
		{0.000001, "0.000001"}, // 1e-6 십진 경계.
		{1e-6, "0.000001"},
		{1e-7, "1e-7"}, // 1e-7 지수 경계(패딩 없음).
		{1e-10, "1e-10"},
		{1e19, "10000000000000000000"},
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},                        // 1e21 지수 경계.
		{9007199254740991, "9007199254740991"}, // 2^53-1
		{9007199254740992, "9007199254740992"}, // 2^53
		{0.30000000000000004, "0.30000000000000004"},        // 최단 왕복.
		{5e-324, "5e-324"},                                  // 최소 subnormal.
		{1.7976931348623157e308, "1.7976931348623157e+308"}, // 최대치.
	}
	for _, tc := range cases {
		if got := NumberString(tc.in); got != tc.want {
			t.Fatalf("NumberString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNumberStringNonFinite는 방어적 비유한 처리 분기를 커버한다(입력엔 오지 않지만).
func TestNumberStringNonFinite(t *testing.T) {
	if got := NumberString(math.NaN()); got != "NaN" {
		t.Fatalf("NaN → %q", got)
	}
	if got := NumberString(math.Inf(1)); got != "Infinity" {
		t.Fatalf("+Inf → %q", got)
	}
	if got := NumberString(math.Inf(-1)); got != "-Infinity" {
		t.Fatalf("-Inf → %q", got)
	}
}

// TestNumberStringMatchesEncodingJSON은 유한수에 대해 Go encoding/json(ECMAScript 호환
// 지수 정리)과도 일치함을 교차확인한다.
func TestNumberStringMatchesEncodingJSON(t *testing.T) {
	vals := []float64{
		0, 1, -1, 42, 100, 123.45, 3.14, 0.5, 1e-6, 1e-7, 1e19, 1e20, 1e21,
		9007199254740991, 9007199254740992, 0.30000000000000004, 6.022e23, 1.5e-12, 7.29e-7,
	}
	for _, v := range vals {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%v): %v", v, err)
		}
		if got := NumberString(v); got != string(b) {
			t.Fatalf("NumberString(%v) = %q, encoding/json = %q", v, got, string(b))
		}
	}
}
