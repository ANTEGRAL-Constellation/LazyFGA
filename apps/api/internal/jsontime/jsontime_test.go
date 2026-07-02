package jsontime

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMarshalJSON_matchesJSDateToISOString(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{
			name: "utc with millis",
			in:   time.Date(2026, 7, 2, 12, 34, 56, 789_000_000, time.UTC),
			want: `"2026-07-02T12:34:56.789Z"`,
		},
		{
			name: "zero fractional prints .000",
			in:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			want: `"2026-01-01T00:00:00.000Z"`,
		},
		{
			name: "sub-millisecond is truncated not rounded",
			in:   time.Date(2026, 7, 2, 1, 2, 3, 4_999_999, time.UTC),
			want: `"2026-07-02T01:02:03.004Z"`,
		},
		{
			name: "non-utc offset is converted to Z",
			in:   time.Date(2026, 7, 2, 21, 0, 0, 500_000_000, time.FixedZone("KST", 9*3600)),
			want: `"2026-07-02T12:00:00.500Z"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(New(tc.in))
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestMarshalJSON_insideStructPreservesField(t *testing.T) {
	type dto struct {
		CreatedAt Time `json:"createdAt"`
	}
	out, err := json.Marshal(dto{CreatedAt: New(time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC))})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"createdAt":"2026-07-02T00:00:00.000Z"}`
	if string(out) != want {
		t.Fatalf("got %s, want %s", out, want)
	}
}

func TestUnmarshalJSON(t *testing.T) {
	t.Run("rfc3339 with Z", func(t *testing.T) {
		var got Time
		if err := json.Unmarshal([]byte(`"2026-07-02T12:34:56.789Z"`), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		want := time.Date(2026, 7, 2, 12, 34, 56, 789_000_000, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("got %v, want %v", got.Time, want)
		}
	})
	t.Run("null leaves value untouched", func(t *testing.T) {
		orig := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		got := New(orig)
		if err := json.Unmarshal([]byte(`null`), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !got.Equal(orig) {
			t.Fatalf("null must not overwrite: got %v", got.Time)
		}
	})
	t.Run("invalid json string type", func(t *testing.T) {
		var got Time
		if err := json.Unmarshal([]byte(`12345`), &got); err == nil {
			t.Fatal("expected error for non-string payload")
		}
	})
	t.Run("unparseable timestamp", func(t *testing.T) {
		var got Time
		if err := json.Unmarshal([]byte(`"not-a-time"`), &got); err == nil {
			t.Fatal("expected error for bad timestamp")
		}
	})
}

func TestNowUTC(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := NowUTC()
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC location, got %v", got.Location())
	}
	if !got.After(before) {
		t.Fatalf("NowUTC not after reference: %v", got.Time)
	}
}

func TestRoundTrip(t *testing.T) {
	orig := New(time.Date(2026, 7, 2, 12, 34, 56, 789_000_000, time.UTC))
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Time
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !orig.Equal(back.Time) {
		t.Fatalf("round trip mismatch: %v vs %v", orig.Time, back.Time)
	}
}
