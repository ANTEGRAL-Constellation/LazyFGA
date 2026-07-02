package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/config"
)

// newHandlerApp는 handler()를 부를 수 있는 최소 App을 만든다(nil pool 허용 —
// 미인증 라우트는 DB를 건드리지 않는다).
func newHandlerApp(t *testing.T, cfg config.Config, ping bool) *App {
	t.Helper()
	a := &App{
		Config:  cfg,
		Logger:  discardLogger(),
		Gateway: &fakeGateway{ping: ping},
	}
	a.applyDefaults()
	return a
}

// TestModuleMounts_unauthorized는 전 모듈 라우트가 마운트됐고, bearer 가드가 걸린 라우트가
// 토큰 없이 401을 돌려주는지 확인한다(미들웨어가 핸들러/DB 이전에 동작하므로 DB 불요).
func TestModuleMounts_unauthorized(t *testing.T) {
	a := newHandlerApp(t, config.Config{Port: 0}, false)
	h := a.handler()

	// 각 모듈의 대표 라우트(마운트가 실제로 걸렸는지 커버).
	cases := []struct{ method, path string }{
		{http.MethodPost, "/model"},
		{http.MethodGet, "/model/current"},
		{http.MethodGet, "/model/versions"},
		{http.MethodGet, "/model/diff?from=a&to=b"},
		{http.MethodGet, "/model/versions/some-id"},
		{http.MethodPost, "/policies"},
		{http.MethodGet, "/policies"},
		{http.MethodGet, "/policies/x"},
		{http.MethodPut, "/policies/x"},
		{http.MethodDelete, "/policies/x"},
		{http.MethodPost, "/tokens"},
		{http.MethodGet, "/tokens"},
		{http.MethodDelete, "/tokens/x"},
		{http.MethodPost, "/grants"},
		{http.MethodDelete, "/grants"},
		{http.MethodGet, "/grants?resource=document:1"},
		{http.MethodGet, "/audit"},
		{http.MethodPost, "/idp/connections"},
		{http.MethodGet, "/idp/connections"},
		{http.MethodPut, "/idp/connections/x"},
		{http.MethodDelete, "/idp/connections/x"},
		{http.MethodGet, "/idp/connections/x/rules"},
		{http.MethodPost, "/idp/connections/x/rules"},
		{http.MethodPut, "/idp/rules/x"},
		{http.MethodDelete, "/idp/rules/x"},
		{http.MethodPost, "/access/v1/evaluation"}, // service|admin.
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401 (mount missing or guard not applied)", tc.method, tc.path, rec.Code)
		}
	}
}

// TestModuleMounts_healthzShape는 /healthz가 TS와 같은 JSON 필드를 돌려주는지 확인한다.
func TestModuleMounts_healthzShape(t *testing.T) {
	a := newHandlerApp(t, config.Config{Port: 0}, false)
	a.storeReady.Store(false)
	h := a.handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// 의존성이 down이므로 degraded(503)여야 한다.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz status = %d, want 503 (degraded)", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("healthz body not JSON: %v", err)
	}
	for _, key := range []string{"status", "version", "db", "openfga", "storeReady"} {
		if _, ok := body[key]; !ok {
			t.Errorf("healthz body missing key %q: %s", key, rec.Body.String())
		}
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
}

// TestModuleMounts_unknownRoute는 미매칭 라우트가 Hono 기본 404 본문을 돌려주는지 확인한다.
func TestModuleMounts_unknownRoute(t *testing.T) {
	a := newHandlerApp(t, config.Config{Port: 0}, false)
	h := a.handler()

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || rec.Body.String() != "404 Not Found" {
		t.Fatalf("unknown route = %d %q, want 404 \"404 Not Found\"", rec.Code, rec.Body.String())
	}
}

// TestModuleMounts_integration은 scratch DB + fake gateway로 전 모듈이 DB·인증까지 관통해
// 정상 응답하는지 확인한다(admin 토큰 200, webhook은 미등록 provider 404, healthz 200).
func TestModuleMounts_integration(t *testing.T) {
	pool := newMigratedScratchDB(t)
	const adminTok = "test-admin-token"
	a := &App{
		Config:  config.Config{Port: 0, AdminToken: adminTok},
		Logger:  discardLogger(),
		Pool:    pool,
		Gateway: &fakeGateway{ping: true},
	}
	a.applyDefaults()
	a.storeReady.Store(true)
	h := a.handler()

	do := func(method, path string, withAdmin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if withAdmin {
			req.Header.Set("Authorization", "Bearer "+adminTok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// healthz: db up(scratch pool) + openfga up(fake ping) + storeReady → 200.
	if rec := do(http.MethodGet, "/healthz", false); rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200 (ready), body=%s", rec.Code, rec.Body.String())
	}

	// admin 토큰으로 DB 관통 GET들 → 200(빈 목록/미발행 not-found 등 모듈별 정상 응답).
	adminGets := []struct {
		path       string
		wantStatus int
	}{
		{"/model/versions", http.StatusOK},
		{"/model/current", http.StatusNotFound}, // 미발행 → 404.
		{"/policies", http.StatusOK},
		{"/tokens", http.StatusOK},
		{"/audit", http.StatusOK},
		{"/idp/connections", http.StatusOK},
	}
	for _, g := range adminGets {
		if rec := do(http.MethodGet, g.path, true); rec.Code != g.wantStatus {
			t.Errorf("GET %s (admin) = %d, want %d, body=%s", g.path, rec.Code, g.wantStatus, rec.Body.String())
		}
	}

	// webhook: 서명 인증 라우트(bearer 불요). 미등록 provider → 404 unknown provider
	// (마운트가 걸렸고 DB 조회까지 도달함을 증명).
	if rec := do(http.MethodPost, "/idp/webhook/zitadel", false); rec.Code != http.StatusNotFound {
		t.Errorf("webhook unknown provider = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}

// TestGuardScopeMatrix는 TS(Hono) 실측 가드 스코프를 고정한다(LFGA-27 리뷰 반영):
// 트레일링 슬래시/미매칭 서브패스도 가드 우선순위(미인증 401 → 인증 404)가 동일해야 한다.
// 인증 케이스는 admin 토큰으로 가드만 통과시키고 라우팅 결과(404)를 본다 — DB 불요.
func TestGuardScopeMatrix(t *testing.T) {
	a := newHandlerApp(t, config.Config{Port: 0, AdminToken: "matrix-admin"}, false)
	h := a.handler()

	cases := []struct {
		method, path string
		auth         bool
		want         int
	}{
		{http.MethodPost, "/model/", false, 401},
		{http.MethodPost, "/model/", true, 404},
		{http.MethodGet, "/model/xyz", false, 401},
		{http.MethodGet, "/model/xyz", true, 404},
		{http.MethodPost, "/policies/", true, 404},
		{http.MethodPost, "/grants/", true, 404},
		{http.MethodGet, "/access/v1/xyz", false, 401},
		{http.MethodGet, "/tokens/a/b", false, 401},
		{http.MethodGet, "/tokens/", false, 401},
		{http.MethodGet, "/tokens/", true, 404},
		{http.MethodGet, "/audit/", false, 401},
		{http.MethodGet, "/audit/", true, 404},
		{http.MethodGet, "/audit/foo", false, 401},
		{http.MethodGet, "/idp/xyz", false, 404},
		{http.MethodGet, "/idp/connections/", false, 401},
		{http.MethodGet, "/idp/connections/", true, 404},
		{http.MethodGet, "/idp/rules", false, 401},
		{http.MethodGet, "/idp/rules/", false, 401},
		{http.MethodGet, "/idp/connections/abc/def", false, 401},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if c.auth {
			req.Header.Set("Authorization", "Bearer matrix-admin")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s auth=%v = %d, want %d", c.method, c.path, c.auth, rec.Code, c.want)
		}
	}
}
