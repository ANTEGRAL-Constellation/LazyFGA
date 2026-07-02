package idp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
	"github.com/go-chi/chi/v5"
)

// ── fakes ──

type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, header string) (httpx.Principal, error) {
	switch header {
	case "Bearer admin":
		return httpx.Principal{Role: httpx.RoleAdmin}, nil
	case "Bearer service":
		return httpx.Principal{Role: httpx.RoleService, TokenID: "tok1"}, nil
	case "Bearer boom":
		return httpx.Principal{}, errors.New("infra down")
	default:
		return httpx.Principal{}, httpx.ErrUnauthorized
	}
}

type recCall struct {
	action string
	actor  string
	data   map[string]any
}

type fakeRecorder struct{ calls []recCall }

func (f *fakeRecorder) Record(action string, data map[string]any, actor string) {
	f.calls = append(f.calls, recCall{action: action, actor: actor, data: data})
}

type fakeGateway struct {
	err  error
	seen []openfga.WriteInput
}

func (f *fakeGateway) Write(_ context.Context, in openfga.WriteInput, _ ...openfga.WriteOption) error {
	f.seen = append(f.seen, in)
	return f.err
}

type fakeRepo struct {
	listConnections func(context.Context) ([]PublicConnection, error)
	getByID         func(context.Context, string) (*PublicConnection, error)
	getByProvider   func(context.Context, string) (*Connection, error)
	createConn      func(context.Context, CreateConnectionInput) (*PublicConnection, error)
	updateConn      func(context.Context, string, ConnectionPatch) (*PublicConnection, error)
	deleteConn      func(context.Context, string) (bool, error)
	listRules       func(context.Context, string) ([]StoredRule, error)
	getRule         func(context.Context, string) (*StoredRule, error)
	rulesByProvider func(context.Context, string) ([]MappingRule, error)
	createRuleFn    func(context.Context, string, CreateRuleInput) (*StoredRule, error)
	updateRuleFn    func(context.Context, string, RulePatch) (*StoredRule, error)
	deleteRuleFn    func(context.Context, string) (bool, error)
}

func (f *fakeRepo) ListConnections(ctx context.Context) ([]PublicConnection, error) {
	return f.listConnections(ctx)
}
func (f *fakeRepo) GetConnectionByID(ctx context.Context, id string) (*PublicConnection, error) {
	if f.getByID == nil {
		return nil, nil // zero-value fake = not found(malformed uuid의 22P02 매핑과 동일 관측).
	}
	return f.getByID(ctx, id)
}
func (f *fakeRepo) GetConnectionByProvider(ctx context.Context, p string) (*Connection, error) {
	return f.getByProvider(ctx, p)
}
func (f *fakeRepo) CreateConnection(ctx context.Context, in CreateConnectionInput) (*PublicConnection, error) {
	return f.createConn(ctx, in)
}
func (f *fakeRepo) UpdateConnection(ctx context.Context, id string, p ConnectionPatch) (*PublicConnection, error) {
	if f.updateConn == nil {
		return nil, nil
	}
	return f.updateConn(ctx, id, p)
}
func (f *fakeRepo) DeleteConnection(ctx context.Context, id string) (bool, error) {
	if f.deleteConn == nil {
		return false, nil
	}
	return f.deleteConn(ctx, id)
}
func (f *fakeRepo) ListRulesByConnection(ctx context.Context, id string) ([]StoredRule, error) {
	return f.listRules(ctx, id)
}
func (f *fakeRepo) GetRuleByID(ctx context.Context, id string) (*StoredRule, error) {
	if f.getRule == nil {
		return nil, nil
	}
	return f.getRule(ctx, id)
}
func (f *fakeRepo) GetRulesByProvider(ctx context.Context, p string) ([]MappingRule, error) {
	return f.rulesByProvider(ctx, p)
}
func (f *fakeRepo) CreateRule(ctx context.Context, id string, in CreateRuleInput) (*StoredRule, error) {
	return f.createRuleFn(ctx, id, in)
}
func (f *fakeRepo) UpdateRule(ctx context.Context, id string, p RulePatch) (*StoredRule, error) {
	return f.updateRuleFn(ctx, id, p)
}
func (f *fakeRepo) DeleteRule(ctx context.Context, id string) (bool, error) {
	if f.deleteRuleFn == nil {
		return false, nil
	}
	return f.deleteRuleFn(ctx, id)
}

// ── helpers ──

const uuidC = "11111111-1111-1111-1111-111111111111"
const uuidR = "22222222-2222-2222-2222-222222222222"

func newRouter(d Deps) *chi.Mux {
	r := chi.NewRouter()
	r.NotFound(httpx.WriteHonoNotFound)
	Mount(r, d)
	return r
}

func do(r chi.Router, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func adminHdr() map[string]string { return map[string]string{"Authorization": "Bearer admin"} }

func assertBody(t *testing.T, w *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if w.Code != code {
		t.Errorf("status = %d, want %d (body=%s)", w.Code, code, w.Body.String())
	}
	if got := w.Body.String(); got != body {
		t.Errorf("body = %s\nwant %s", got, body)
	}
}

func baseDeps() (*fakeRepo, *fakeRecorder, *fakeGateway, Deps) {
	repo := &fakeRepo{}
	rec := &fakeRecorder{}
	gw := &fakeGateway{}
	return repo, rec, gw, Deps{Repo: repo, Gateway: gw, Recorder: rec, Auth: fakeAuth{}, Now: fixedNow}
}

func zitadelConn() *Connection {
	return &Connection{ID: uuidC, Provider: "zitadel", Preset: nil, SigningSecret: "topsecret", Enabled: true}
}

// ── webhook ──

func TestWebhook_UnknownProvider(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return nil, nil }
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", "{}", nil)
	assertBody(t, w, http.StatusNotFound, `{"error":"unknown provider"}`)
}

func TestWebhook_ProviderLookupError(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return nil, errors.New("db down") }
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", "{}", nil)
	if w.Code != http.StatusInternalServerError || w.Body.String() != "Internal Server Error" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}

func TestWebhook_Disabled(t *testing.T) {
	repo, _, _, d := baseDeps()
	c := zitadelConn()
	c.Enabled = false
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return c, nil }
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", "{}", nil)
	assertBody(t, w, http.StatusForbidden, `{"error":"connection disabled"}`)
}

func TestWebhook_UnknownPreset(t *testing.T) {
	repo, rec, _, d := baseDeps()
	c := zitadelConn()
	bogus := "bogus"
	c.Preset = &bogus
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return c, nil }
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", "{}", nil)
	assertBody(t, w, http.StatusInternalServerError, `{"error":"connection misconfigured (unknown preset)"}`)
	if len(rec.calls) != 0 {
		t.Error("unknown preset must not write DB audit")
	}
}

func TestWebhook_InvalidSignature_NoAudit(t *testing.T) {
	repo, rec, _, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", "{}", map[string]string{"ZITADEL-Signature": "t=1,v1=deadbeef"})
	assertBody(t, w, http.StatusUnauthorized, `{"error":"invalid signature"}`)
	if len(rec.calls) != 0 {
		t.Error("invalid signature must NOT write DB audit (amplification guard)")
	}
}

func signedZitadel(body string) map[string]string {
	ts := strconv.FormatInt(testNowSec, 10)
	sig := "t=" + ts + ",v1=" + hmacHex("topsecret", []byte(ts+"."), []byte(body))
	return map[string]string{"ZITADEL-Signature": sig}
}

func TestWebhook_InvalidJSON(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	body := "not-json"
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	assertBody(t, w, http.StatusBadRequest, `{"error":"invalid json body"}`)
}

func TestWebhook_NoEvents(t *testing.T) {
	repo, rec, _, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	body := `{"event_type":"session.created","aggregateID":"x"}`
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	assertBody(t, w, http.StatusOK, `{"applied":0,"skipped":0,"failed":0}`)
	if len(rec.calls) != 1 || rec.calls[0].action != "idp.webhook.no_events" || rec.calls[0].actor != "idp:zitadel" {
		t.Fatalf("audit = %+v", rec.calls)
	}
	if rec.calls[0].data["eventType"] != "session.created" || rec.calls[0].data["provider"] != "zitadel" {
		t.Errorf("no_events data = %+v", rec.calls[0].data)
	}
}

func grantRules() []MappingRule {
	return []MappingRule{{
		EventType:     "user.grant.added",
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"},
		Op:            "write",
	}}
}

func TestWebhook_ApplyWriteSuccess(t *testing.T) {
	repo, rec, gw, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	repo.rulesByProvider = func(context.Context, string) ([]MappingRule, error) { return grantRules(), nil }
	body := `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng"}}`
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	assertBody(t, w, http.StatusOK, `{"applied":1,"skipped":0,"failed":0}`)
	if len(gw.seen) != 1 || len(gw.seen[0].Writes) != 1 {
		t.Fatalf("gateway writes = %+v", gw.seen)
	}
	if gw.seen[0].Writes[0].Object != "team:eng" {
		t.Errorf("write object = %q", gw.seen[0].Writes[0].Object)
	}
	if len(rec.calls) != 1 || rec.calls[0].action != "idp.tuple.write" {
		t.Errorf("audit = %+v", rec.calls)
	}
}

func TestWebhook_IdempotentSkipped(t *testing.T) {
	repo, _, gw, d := baseDeps()
	gw.err = errors.New("write_failed_due_to_invalid_input: cannot write a tuple which already exists")
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	repo.rulesByProvider = func(context.Context, string) ([]MappingRule, error) { return grantRules(), nil }
	body := `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng"}}`
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	assertBody(t, w, http.StatusOK, `{"applied":0,"skipped":1,"failed":0}`)
}

func TestWebhook_DeterministicFailed(t *testing.T) {
	repo, _, gw, d := baseDeps()
	gw.err = errors.New("relation not found for object doc:1")
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	repo.rulesByProvider = func(context.Context, string) ([]MappingRule, error) { return grantRules(), nil }
	body := `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng"}}`
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	assertBody(t, w, http.StatusOK, `{"applied":0,"skipped":0,"failed":1}`)
}

func TestWebhook_TransientUpstream(t *testing.T) {
	repo, _, gw, d := baseDeps()
	gw.err = errors.New("fetch failed")
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	repo.rulesByProvider = func(context.Context, string) ([]MappingRule, error) { return grantRules(), nil }
	body := `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng"}}`
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	assertBody(t, w, http.StatusBadGateway, `{"error":"upstream unavailable"}`)
}

func TestWebhook_RulesLookupError(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	repo.rulesByProvider = func(context.Context, string) ([]MappingRule, error) { return nil, errors.New("db") }
	body := `{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng"}}`
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.Code)
	}
}

func TestWebhook_DeleteOpSuccess(t *testing.T) {
	repo, _, gw, d := baseDeps()
	repo.getByProvider = func(context.Context, string) (*Connection, error) { return zitadelConn(), nil }
	repo.rulesByProvider = func(context.Context, string) ([]MappingRule, error) {
		return []MappingRule{{
			EventType:     "user.grant.removed",
			TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"},
			Op:            "delete",
		}}, nil
	}
	body := `{"event_type":"user.grant.removed","event_payload":{"userId":"alice","projectId":"eng"}}`
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", body, signedZitadel(body))
	assertBody(t, w, http.StatusOK, `{"applied":1,"skipped":0,"failed":0}`)
	if len(gw.seen) != 1 || len(gw.seen[0].Deletes) != 1 {
		t.Fatalf("expected 1 delete, got %+v", gw.seen)
	}
}

func TestWebhook_BodyTooLarge(t *testing.T) {
	repo, _, _, d := baseDeps()
	called := false
	repo.getByProvider = func(context.Context, string) (*Connection, error) { called = true; return zitadelConn(), nil }
	big := strings.Repeat("a", maxWebhookBody+1)
	w := do(newRouter(d), "POST", "/idp/webhook/zitadel", big, nil)
	assertBody(t, w, http.StatusRequestEntityTooLarge, `{"error":"payload too large"}`)
	if called {
		t.Error("413 must fire before connection lookup")
	}
}

// ── connections CRUD ──

func TestConnections_Auth(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.listConnections = func(context.Context) ([]PublicConnection, error) { return nil, nil }
	t.Run("401 without token", func(t *testing.T) {
		w := do(newRouter(d), "GET", "/idp/connections", "", nil)
		assertBody(t, w, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	})
	t.Run("403 with service token", func(t *testing.T) {
		w := do(newRouter(d), "GET", "/idp/connections", "", map[string]string{"Authorization": "Bearer service"})
		assertBody(t, w, http.StatusForbidden, `{"error":"forbidden"}`)
	})
	t.Run("500 on auth infra error", func(t *testing.T) {
		w := do(newRouter(d), "GET", "/idp/connections", "", map[string]string{"Authorization": "Bearer boom"})
		if w.Code != http.StatusInternalServerError || w.Body.String() != "Internal Server Error" {
			t.Fatalf("got %d %q", w.Code, w.Body.String())
		}
	})
}

func TestListConnections(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.listConnections = func(context.Context) ([]PublicConnection, error) {
		return []PublicConnection{{ID: uuidC, Provider: "zitadel", Preset: nil, Enabled: true}}, nil
	}
	w := do(newRouter(d), "GET", "/idp/connections", "", adminHdr())
	assertBody(t, w, http.StatusOK, `{"connections":[{"id":"`+uuidC+`","provider":"zitadel","preset":null,"enabled":true}]}`)
}

func TestListConnections_Error(t *testing.T) {
	repo, _, _, d := baseDeps()
	repo.listConnections = func(context.Context) ([]PublicConnection, error) { return nil, errors.New("db") }
	w := do(newRouter(d), "GET", "/idp/connections", "", adminHdr())
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d", w.Code)
	}
}

func TestCreateConnection(t *testing.T) {
	t.Run("422 missing provider/secret", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "POST", "/idp/connections", `{"provider":"  "}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"non-empty provider and signingSecret are required"}`)
	})
	t.Run("422 invalid json", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "POST", "/idp/connections", `nope`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"non-empty provider and signingSecret are required"}`)
	})
	t.Run("422 unknown preset", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "POST", "/idp/connections", `{"provider":"p","signingSecret":"s","preset":"nope"}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"unknown preset; known: zitadel, standard-webhooks"}`)
	})
	t.Run("409 duplicate", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.createConn = func(context.Context, CreateConnectionInput) (*PublicConnection, error) {
			return nil, ErrDuplicateProvider
		}
		w := do(newRouter(d), "POST", "/idp/connections", `{"provider":"zitadel","signingSecret":"s"}`, adminHdr())
		assertBody(t, w, http.StatusConflict, `{"error":"provider already exists"}`)
	})
	t.Run("500 repo error", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.createConn = func(context.Context, CreateConnectionInput) (*PublicConnection, error) { return nil, errors.New("db") }
		w := do(newRouter(d), "POST", "/idp/connections", `{"provider":"zitadel","signingSecret":"s"}`, adminHdr())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("got %d", w.Code)
		}
	})
	t.Run("201 success + audit", func(t *testing.T) {
		repo, rec, _, d := baseDeps()
		var got CreateConnectionInput
		repo.createConn = func(_ context.Context, in CreateConnectionInput) (*PublicConnection, error) {
			got = in
			return &PublicConnection{ID: uuidC, Provider: "zitadel", Preset: strp("zitadel"), Enabled: true}, nil
		}
		w := do(newRouter(d), "POST", "/idp/connections", `{"provider":"zitadel","signingSecret":"s","preset":"zitadel","enabled":true}`, adminHdr())
		assertBody(t, w, http.StatusCreated, `{"connection":{"id":"`+uuidC+`","provider":"zitadel","preset":"zitadel","enabled":true}}`)
		if got.Provider != "zitadel" || got.SigningSecret != "s" || got.Preset == nil || *got.Preset != "zitadel" || !got.Enabled {
			t.Errorf("create input = %+v", got)
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "idp.connection.create" || rec.calls[0].actor != "admin" {
			t.Errorf("audit = %+v", rec.calls)
		}
	})
	t.Run("201 default enabled true, no preset", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		var got CreateConnectionInput
		repo.createConn = func(_ context.Context, in CreateConnectionInput) (*PublicConnection, error) {
			got = in
			return &PublicConnection{ID: uuidC, Provider: "custom", Preset: nil, Enabled: true}, nil
		}
		w := do(newRouter(d), "POST", "/idp/connections", `{"provider":"custom","signingSecret":"s"}`, adminHdr())
		if w.Code != http.StatusCreated || got.Enabled != true || got.Preset != nil {
			t.Fatalf("code=%d input=%+v", w.Code, got)
		}
	})
}

func TestUpdateConnection(t *testing.T) {
	okConn := &PublicConnection{ID: uuidC, Provider: "zitadel", Preset: nil, Enabled: true}
	t.Run("404 malformed uuid", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "PUT", "/idp/connections/not-a-uuid", `{}`, adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
	t.Run("404 not found", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return nil, nil }
		w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{}`, adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
	t.Run("422 signingSecret empty", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return okConn, nil }
		w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{"signingSecret":""}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"signingSecret must be a non-empty string"}`)
	})
	t.Run("422 enabled not bool", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return okConn, nil }
		w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{"enabled":"yes"}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"enabled must be a boolean"}`)
	})
	t.Run("422 unknown preset", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return okConn, nil }
		w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{"preset":"nope"}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"unknown preset; known: zitadel, standard-webhooks"}`)
	})
	t.Run("200 success + audit (invalid json body → no-op patch)", func(t *testing.T) {
		repo, rec, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return okConn, nil }
		var patch ConnectionPatch
		repo.updateConn = func(_ context.Context, _ string, p ConnectionPatch) (*PublicConnection, error) {
			patch = p
			return &PublicConnection{ID: uuidC, Provider: "zitadel", Preset: strp("zitadel"), Enabled: false}, nil
		}
		w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{"enabled":false,"preset":"zitadel","signingSecret":"new"}`, adminHdr())
		assertBody(t, w, http.StatusOK, `{"connection":{"id":"`+uuidC+`","provider":"zitadel","preset":"zitadel","enabled":false}}`)
		if patch.Preset == nil || *patch.Preset != "zitadel" || patch.SigningSecret == nil || patch.Enabled == nil || *patch.Enabled {
			t.Errorf("patch = %+v", patch)
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "idp.connection.update" {
			t.Errorf("audit = %+v", rec.calls)
		}
	})
	t.Run("404 lost race (update returns nil)", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return okConn, nil }
		repo.updateConn = func(context.Context, string, ConnectionPatch) (*PublicConnection, error) { return nil, nil }
		w := do(newRouter(d), "PUT", "/idp/connections/"+uuidC, `{}`, adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
}

func TestDeleteConnection(t *testing.T) {
	t.Run("404 malformed uuid", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "DELETE", "/idp/connections/bad", "", adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
	t.Run("404 not found", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.deleteConn = func(context.Context, string) (bool, error) { return false, nil }
		w := do(newRouter(d), "DELETE", "/idp/connections/"+uuidC, "", adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
	t.Run("204 success + audit", func(t *testing.T) {
		repo, rec, _, d := baseDeps()
		repo.deleteConn = func(context.Context, string) (bool, error) { return true, nil }
		w := do(newRouter(d), "DELETE", "/idp/connections/"+uuidC, "", adminHdr())
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Fatalf("got %d body=%q", w.Code, w.Body.String())
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "idp.connection.delete" {
			t.Errorf("audit = %+v", rec.calls)
		}
	})
}

// ── rules CRUD ──

func TestListRules(t *testing.T) {
	t.Run("404 not found connection", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return nil, nil }
		w := do(newRouter(d), "GET", "/idp/connections/"+uuidC+"/rules", "", adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
	t.Run("200 list", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) {
			return &PublicConnection{ID: uuidC, Provider: "zitadel", Enabled: true}, nil
		}
		repo.listRules = func(context.Context, string) ([]StoredRule, error) {
			return []StoredRule{{
				ID: uuidR, ConnectionID: uuidC, EventType: "user.grant.added", Match: json.RawMessage("[]"),
				TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"},
				Op:            "write", Priority: 0,
			}}, nil
		}
		w := do(newRouter(d), "GET", "/idp/connections/"+uuidC+"/rules", "", adminHdr())
		assertBody(t, w, http.StatusOK, `{"rules":[{"id":"`+uuidR+`","connectionId":"`+uuidC+`","eventType":"user.grant.added","match":[],"tupleTemplate":{"user":"user:{{subject}}","object":"team:{{attributes.project}}","relation":"member"},"op":"write","priority":0}]}`)
	})
}

func TestCreateRule(t *testing.T) {
	conn := &PublicConnection{ID: uuidC, Provider: "zitadel", Enabled: true}
	setup := func() (*fakeRepo, *fakeRecorder, Deps) {
		repo, rec, _, d := baseDeps()
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return conn, nil }
		return repo, rec, d
	}
	t.Run("404 malformed uuid", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "POST", "/idp/connections/bad/rules", `{}`, adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"connection not found"}`)
	})
	t.Run("422 shape (missing fields)", func(t *testing.T) {
		_, _, d := setup()
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", `{"eventType":"x"}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"`+ruleShapeError+`"}`)
	})
	t.Run("422 invalid json", func(t *testing.T) {
		_, _, d := setup()
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", `nope`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"`+ruleShapeError+`"}`)
	})
	t.Run("422 non-integer priority", func(t *testing.T) {
		_, _, d := setup()
		body := `{"eventType":"x","op":"write","tupleTemplate":{"user":"user:a","object":"team:1","relation":"member"},"priority":1.5}`
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", body, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"`+ruleShapeError+`"}`)
	})
	t.Run("422 fanOut not attribute", func(t *testing.T) {
		_, _, d := setup()
		body := `{"eventType":"user.grant.added","op":"write","tupleTemplate":{"user":"user:{{subject}}","object":"team:1","relation":"{{item}}"},"fanOut":"nope"}`
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", body, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"fanOut \"nope\" is not an attribute produced by preset for event \"user.grant.added\" (known: project, roleKeys)"}`)
	})
	t.Run("422 item without fanOut", func(t *testing.T) {
		_, _, d := setup()
		body := `{"eventType":"user.grant.added","op":"write","tupleTemplate":{"user":"user:{{subject}}","object":"team:1","relation":"{{item}}"}}`
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", body, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"tuple template references {{item}} but no fanOut is set"}`)
	})
	t.Run("201 success + audit", func(t *testing.T) {
		repo, rec, d := setup()
		var got CreateRuleInput
		repo.createRuleFn = func(_ context.Context, _ string, in CreateRuleInput) (*StoredRule, error) {
			got = in
			// fake는 Postgres jsonb 정규화(키 길이→바이트순, compact)를 흉내낸다.
			return &StoredRule{ID: uuidR, ConnectionID: uuidC, EventType: in.EventType,
				Match:         json.RawMessage(`[{"field":"subject","equals":"alice"}]`),
				TupleTemplate: in.TupleTemplate, Op: in.Op, Priority: in.Priority, FanOut: in.FanOut}, nil
		}
		body := `{"eventType":"user.grant.added","op":"write","tupleTemplate":{"user":"user:{{subject}}","object":"team:{{attributes.project}}","relation":"{{item}}"},"fanOut":"roleKeys","match":[{"field":"subject","equals":"alice"}],"priority":5}`
		w := do(newRouter(d), "POST", "/idp/connections/"+uuidC+"/rules", body, adminHdr())
		assertBody(t, w, http.StatusCreated, `{"rule":{"id":"`+uuidR+`","connectionId":"`+uuidC+`","eventType":"user.grant.added","match":[{"field":"subject","equals":"alice"}],"tupleTemplate":{"user":"user:{{subject}}","object":"team:{{attributes.project}}","relation":"{{item}}"},"op":"write","priority":5,"fanOut":"roleKeys"}}`)
		var gm []MatchPredicate
		if err := json.Unmarshal(got.Match, &gm); err != nil {
			t.Fatalf("captured match: %v", err)
		}
		if got.Priority != 5 || got.FanOut == nil || *got.FanOut != "roleKeys" || len(gm) != 1 {
			t.Errorf("create input = %+v", got)
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "idp.rule.create" {
			t.Errorf("audit = %+v", rec.calls)
		}
	})
}

func TestUpdateRule(t *testing.T) {
	existing := &StoredRule{ID: uuidR, ConnectionID: uuidC, EventType: "user.grant.added", Match: json.RawMessage("[]"),
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"}, Op: "write", Priority: 0}
	conn := &PublicConnection{ID: uuidC, Provider: "zitadel", Enabled: true}
	setup := func() (*fakeRepo, *fakeRecorder, Deps) {
		repo, rec, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return existing, nil }
		repo.getByID = func(context.Context, string) (*PublicConnection, error) { return conn, nil }
		return repo, rec, d
	}
	t.Run("404 malformed uuid", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "PUT", "/idp/rules/bad", `{}`, adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"rule not found"}`)
	})
	t.Run("404 not found", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.getRule = func(context.Context, string) (*StoredRule, error) { return nil, nil }
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{}`, adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"rule not found"}`)
	})
	t.Run("422 op invalid", func(t *testing.T) {
		_, _, d := setup()
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"op":"nope"}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"op must be write|delete"}`)
	})
	t.Run("422 match invalid", func(t *testing.T) {
		_, _, d := setup()
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"match":"nope"}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"invalid match[]"}`)
	})
	t.Run("422 priority not integer", func(t *testing.T) {
		_, _, d := setup()
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"priority":1.5}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"priority must be an integer"}`)
	})
	t.Run("422 tupleTemplate invalid", func(t *testing.T) {
		_, _, d := setup()
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"tupleTemplate":{"user":1}}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"invalid tupleTemplate"}`)
	})
	t.Run("422 fanOut invalid", func(t *testing.T) {
		_, _, d := setup()
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"fanOut":"   "}`, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"fanOut must be a non-empty string or null"}`)
	})
	t.Run("422 merged fanOutError (template refs item, fanOut cleared)", func(t *testing.T) {
		_, _, d := setup()
		body := `{"tupleTemplate":{"user":"user:{{subject}}","object":"team:1","relation":"{{item}}"},"fanOut":null}`
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, body, adminHdr())
		assertBody(t, w, http.StatusUnprocessableEntity, `{"error":"tuple template references {{item}} but no fanOut is set"}`)
	})
	t.Run("200 success + audit", func(t *testing.T) {
		repo, rec, d := setup()
		var patch RulePatch
		repo.updateRuleFn = func(_ context.Context, _ string, p RulePatch) (*StoredRule, error) {
			patch = p
			return &StoredRule{ID: uuidR, ConnectionID: uuidC, EventType: "user.grant.added", Match: json.RawMessage("[]"),
				TupleTemplate: existing.TupleTemplate, Op: "delete", Priority: 3}, nil
		}
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"op":"delete","priority":3}`, adminHdr())
		assertBody(t, w, http.StatusOK, `{"rule":{"id":"`+uuidR+`","connectionId":"`+uuidC+`","eventType":"user.grant.added","match":[],"tupleTemplate":{"user":"user:{{subject}}","object":"team:{{attributes.project}}","relation":"member"},"op":"delete","priority":3}}`)
		if patch.Op == nil || *patch.Op != "delete" || patch.Priority == nil || *patch.Priority != 3 {
			t.Errorf("patch = %+v", patch)
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "idp.rule.update" {
			t.Errorf("audit = %+v", rec.calls)
		}
	})
	t.Run("404 lost race", func(t *testing.T) {
		repo, _, d := setup()
		repo.updateRuleFn = func(context.Context, string, RulePatch) (*StoredRule, error) { return nil, nil }
		w := do(newRouter(d), "PUT", "/idp/rules/"+uuidR, `{"priority":3}`, adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"rule not found"}`)
	})
}

func TestDeleteRule(t *testing.T) {
	t.Run("404 malformed uuid", func(t *testing.T) {
		_, _, _, d := baseDeps()
		w := do(newRouter(d), "DELETE", "/idp/rules/bad", "", adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"rule not found"}`)
	})
	t.Run("404 not found", func(t *testing.T) {
		repo, _, _, d := baseDeps()
		repo.deleteRuleFn = func(context.Context, string) (bool, error) { return false, nil }
		w := do(newRouter(d), "DELETE", "/idp/rules/"+uuidR, "", adminHdr())
		assertBody(t, w, http.StatusNotFound, `{"error":"rule not found"}`)
	})
	t.Run("204 success + audit", func(t *testing.T) {
		repo, rec, _, d := baseDeps()
		repo.deleteRuleFn = func(context.Context, string) (bool, error) { return true, nil }
		w := do(newRouter(d), "DELETE", "/idp/rules/"+uuidR, "", adminHdr())
		if w.Code != http.StatusNoContent {
			t.Fatalf("got %d", w.Code)
		}
		if len(rec.calls) != 1 || rec.calls[0].action != "idp.rule.delete" {
			t.Errorf("audit = %+v", rec.calls)
		}
	})
}
