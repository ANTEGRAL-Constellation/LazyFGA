package auditread

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// ── fakes ──

type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, header string) (httpx.Principal, error) {
	switch header {
	case "Bearer admin":
		return httpx.Principal{Role: httpx.RoleAdmin}, nil
	case "Bearer service":
		return httpx.Principal{Role: httpx.RoleService, TokenID: "t"}, nil
	default:
		return httpx.Principal{}, httpx.ErrUnauthorized
	}
}

type fakeQueryRepo struct {
	last   Query
	result Result
	err    error
}

func (f *fakeQueryRepo) Query(_ context.Context, q Query) (Result, error) {
	f.last = q
	return f.result, f.err
}

func newRouter(repo QueryRepo) *chi.Mux {
	r := chi.NewRouter()
	r.NotFound(httpx.WriteHonoNotFound)
	Mount(r, Deps{Repo: repo, Auth: fakeAuth{}})
	return r
}

func get(r chi.Router, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func adminHdr() map[string]string { return map[string]string{"Authorization": "Bearer admin"} }

// ── jsStringToNumber / parseLimit ──

func TestJSStringToNumber(t *testing.T) {
	cases := map[string]float64{
		"1.5":       1.5,
		"0":         0,
		"1000":      1000,
		"-5":        -5,
		"":          0,
		"  10  ":    10,
		"1e2":       100,
		"0x10":      16,
		"0o17":      15,
		"0b101":     5,
		"Infinity":  math.Inf(1),
		"+Infinity": math.Inf(1),
		"-Infinity": math.Inf(-1),
	}
	for in, want := range cases {
		got := jsStringToNumber(in)
		if math.IsInf(want, 0) {
			if got != want {
				t.Errorf("jsStringToNumber(%q) = %v, want %v", in, got, want)
			}
		} else if got != want {
			t.Errorf("jsStringToNumber(%q) = %v, want %v", in, got, want)
		}
	}
	for _, nan := range []string{"abc", "1.2.3", "0xzz", "1e", "0b12"} {
		if !math.IsNaN(jsStringToNumber(nan)) {
			t.Errorf("jsStringToNumber(%q) should be NaN", nan)
		}
	}
}

func TestParseLimit(t *testing.T) {
	cases := map[string]int{
		"1.5":       1,
		"abc":       50,
		"0":         50,
		"1000":      200,
		"-5":        1,
		"":          50,
		"50":        50,
		"200":       200,
		"201":       200,
		"Infinity":  200,
		"-Infinity": 1,
		"1":         1,
	}
	for in, want := range cases {
		if got := parseLimit(in); got != want {
			t.Errorf("parseLimit(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDateParam(t *testing.T) {
	t.Run("absent → nil,true", func(t *testing.T) {
		tm, ok := parseDateParam(url.Values{}, "from")
		if tm != nil || !ok {
			t.Errorf("got %v,%v", tm, ok)
		}
	})
	t.Run("present valid → &t,true", func(t *testing.T) {
		tm, ok := parseDateParam(url.Values{"from": {"2026-07-02"}}, "from")
		if tm == nil || !ok {
			t.Errorf("got %v,%v", tm, ok)
		}
	})
	t.Run("present empty → nil,false", func(t *testing.T) {
		tm, ok := parseDateParam(url.Values{"from": {""}}, "from")
		if tm != nil || ok {
			t.Errorf("got %v,%v", tm, ok)
		}
	})
	t.Run("present invalid → nil,false", func(t *testing.T) {
		_, ok := parseDateParam(url.Values{"to": {"nope"}}, "to")
		if ok {
			t.Error("invalid must be false")
		}
	})
}

// ── handleQuery ──

func TestHandleQuery_AuthGuard(t *testing.T) {
	r := newRouter(&fakeQueryRepo{})
	if w := get(r, "/audit", nil); w.Code != http.StatusUnauthorized || w.Body.String() != `{"error":"unauthorized"}` {
		t.Errorf("no token → %d %s", w.Code, w.Body.String())
	}
	if w := get(r, "/audit", map[string]string{"Authorization": "Bearer service"}); w.Code != http.StatusForbidden {
		t.Errorf("service → %d", w.Code)
	}
}

func TestHandleQuery_Success(t *testing.T) {
	repo := &fakeQueryRepo{result: Result{
		Entries: []Entry{{
			ID: "e1", OccurredAt: "2026-07-02T12:00:00.123Z", Actor: "admin", Action: "token.create",
			Data: json.RawMessage(`{"id":"t1","name":"ci"}`),
		}},
		NextCursor: "CURSOR",
	}}
	w := get(newRouter(repo), "/audit?limit=5&action=token.*&actor=admin&from=2026-07-02&to=2026-07-03", adminHdr())
	want := `{"entries":[{"id":"e1","occurredAt":"2026-07-02T12:00:00.123Z","actor":"admin","action":"token.create","data":{"id":"t1","name":"ci"}}],"nextCursor":"CURSOR"}`
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Fatalf("got %d %s\nwant %s", w.Code, w.Body.String(), want)
	}
	// captured query reflects parsed params.
	if repo.last.Limit != 5 || repo.last.Action != "token.*" || repo.last.Actor != "admin" || repo.last.From == nil || repo.last.To == nil {
		t.Errorf("captured query = %+v", repo.last)
	}
}

func TestHandleQuery_NoNextCursor(t *testing.T) {
	repo := &fakeQueryRepo{result: Result{Entries: []Entry{}}}
	w := get(newRouter(repo), "/audit", adminHdr())
	if w.Code != http.StatusOK || w.Body.String() != `{"entries":[]}` {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}

func TestHandleQuery_ValidCursor(t *testing.T) {
	repo := &fakeQueryRepo{result: Result{Entries: []Entry{}}}
	cur := encodeCursor("2026-07-02T12:00:00.000Z", "id1")
	w := get(newRouter(repo), "/audit?cursor="+cur, adminHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if repo.last.Cursor == nil || repo.last.Cursor.ID != "id1" {
		t.Errorf("cursor not parsed: %+v", repo.last.Cursor)
	}
}

func TestHandleQuery_Errors(t *testing.T) {
	t.Run("invalid cursor → 400", func(t *testing.T) {
		w := get(newRouter(&fakeQueryRepo{}), "/audit?cursor=!!!", adminHdr())
		if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"invalid cursor"}` {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("invalid from → 400", func(t *testing.T) {
		w := get(newRouter(&fakeQueryRepo{}), "/audit?from=nope", adminHdr())
		if w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"invalid from/to (use ISO 8601)"}` {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("invalid to → 400", func(t *testing.T) {
		w := get(newRouter(&fakeQueryRepo{}), "/audit?to=nope", adminHdr())
		if w.Code != http.StatusBadRequest {
			t.Fatalf("got %d", w.Code)
		}
	})
	t.Run("repo error → 500", func(t *testing.T) {
		w := get(newRouter(&fakeQueryRepo{err: context.DeadlineExceeded}), "/audit", adminHdr())
		if w.Code != http.StatusInternalServerError || w.Body.String() != "Internal Server Error" {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
}
