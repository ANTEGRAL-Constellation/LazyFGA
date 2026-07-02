package jsutil

import (
	"testing"
)

// MarshalJSON: JSON.stringify escape rules (<>& raw, U+2028/29 raw).
// All literals use Go escape sequences (no raw non-ASCII in source).
func TestMarshalJSON_jsEscapes(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"html-raw", "a<b>&c", "\"a<b>&c\""},
		{"line-seps-raw", "l\u2028p\u2029e", "\"l\u2028p\u2029e\""},
		{"literal-backslash-u2028-text", "x\\u2028y", "\"x\\\\u2028y\""},
		{"newline-kept", "a\nb", "\"a\\nb\""},
		{"escaped-backslash-then-real-sep", "\\\u2028", "\"\\\\\u2028\""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MarshalJSON(tc.in)
			if err != nil {
				t.Fatalf("MarshalJSON: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TrimJS: ES WhiteSpace + LineTerminator set (includes U+FEFF, excludes U+0085).
func TestTrimJS(t *testing.T) {
	if got := TrimJS("\ufeff\u00a0 x\u3000\u2028 "); got != "x" {
		t.Errorf("TrimJS feff/nbsp/ideographic/ls: got %q", got)
	}
	if got := TrimJS("\u0085x\u0085"); got != "\u0085x\u0085" {
		t.Errorf("TrimJS must keep U+0085 (JS does): got %q", got)
	}
	if got := TrimJS(" \t\v\f\r\n y \t\v\f\r\n"); got != "y" {
		t.Errorf("TrimJS line terminators: got %q", got)
	}
}
