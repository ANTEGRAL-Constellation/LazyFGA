package auditread

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestIsoMillis(t *testing.T) {
	tm := time.Date(2026, 7, 2, 12, 0, 0, 123_456_789, time.UTC)
	if got := isoMillis(tm); got != "2026-07-02T12:00:00.123Z" {
		t.Errorf("isoMillis = %q", got)
	}
	// non-UTC input is normalized to UTC 'Z'.
	loc := time.FixedZone("KST", 9*3600)
	tm2 := time.Date(2026, 7, 2, 21, 0, 0, 0, loc)
	if got := isoMillis(tm2); got != "2026-07-02T12:00:00.000Z" {
		t.Errorf("isoMillis(non-UTC) = %q", got)
	}
}

func TestParseFlexibleTime(t *testing.T) {
	ok := []string{
		"2026-07-02T12:00:00Z",
		"2026-07-02T12:00:00.123Z",
		"2026-07-02T12:00:00+09:00",
		"2026-07-02",
	}
	for _, s := range ok {
		if _, valid := parseFlexibleTime(s); !valid {
			t.Errorf("%q should parse", s)
		}
	}
	// date-only is UTC midnight.
	if tm, _ := parseFlexibleTime("2026-07-02"); !tm.Equal(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date-only not UTC midnight: %v", tm)
	}
	bad := []string{"nope", "2026-07-02T12:00:00", "", "12:00:00"}
	for _, s := range bad {
		if _, valid := parseFlexibleTime(s); valid {
			t.Errorf("%q should not parse", s)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	iso := "2026-07-02T12:00:00.123Z"
	enc := encodeCursor(iso, "id-1")
	c, ok := decodeCursor(enc)
	if !ok || c.ID != "id-1" {
		t.Fatalf("decode = %+v,%v", c, ok)
	}
	if !c.OccurredAt.Equal(time.Date(2026, 7, 2, 12, 0, 0, 123_000_000, time.UTC)) {
		t.Errorf("occurredAt = %v", c.OccurredAt)
	}
}

func TestDecodeCursor_Invalid(t *testing.T) {
	if _, ok := decodeCursor("!!!not-base64!!!"); ok {
		t.Error("invalid base64 must fail")
	}
	if _, ok := decodeCursor(base64.RawURLEncoding.EncodeToString([]byte("only-iso-no-id"))); ok {
		t.Error("missing id must fail")
	}
	if _, ok := decodeCursor(base64.RawURLEncoding.EncodeToString([]byte("|id"))); ok {
		t.Error("empty iso must fail")
	}
	if _, ok := decodeCursor(base64.RawURLEncoding.EncodeToString([]byte("not-a-date|id"))); ok {
		t.Error("bad date must fail")
	}
}

func TestDecodeCursor_ToleratesPadding(t *testing.T) {
	iso := "2026-07-02T12:00:00.000Z"
	padded := base64.URLEncoding.EncodeToString([]byte(iso + "|id-1")) // 패딩 포함.
	c, ok := decodeCursor(padded)
	if !ok || c.ID != "id-1" {
		t.Fatalf("padded cursor decode = %+v,%v", c, ok)
	}
}
