package democli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/modules/idp"
)

// recordedReq는 fake API가 받은 요청 스냅샷이다.
type recordedReq struct {
	method string
	path   string
	body   string
	auth   string
	sig    string
}

// apiStub는 데모가 호출하는 lazyFGA API를 흉내낸다. 시나리오/오류 주입 노브를 갖는다.
type apiStub struct {
	connExists    bool   // POST /idp/connections → 409(이미 존재) 시나리오.
	emptyList     bool   // GET /idp/connections → 빈 목록(zitadel 없음).
	existingRule  string // GET rules가 돌려줄 기존 규칙 id("" = 없음).
	evalDecision  bool   // /access/v1/evaluation 응답 decision.
	evalReason    string // reason.text("" = context 없음).
	failPublish   bool   // POST /model → 500.
	healthDown    bool   // GET /healthz → 503.
	badCreateJSON bool   // POST /idp/connections 201 본문을 깨진 JSON으로.
	badRulesJSON  bool   // GET .../rules 본문을 깨진 JSON으로.
	policyStatus  int    // POST /policies 응답 코드(0 → 201).
	verifySig     func([]byte, string) bool

	mu       sync.Mutex
	requests []recordedReq
}

func (s *apiStub) record(r *http.Request, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, recordedReq{
		method: r.Method,
		path:   r.URL.Path,
		body:   string(body),
		auth:   r.Header.Get("Authorization"),
		sig:    r.Header.Get("ZITADEL-Signature"),
	})
}

func (s *apiStub) seen(method, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.requests {
		if r.method == method && r.path == path {
			return true
		}
	}
	return false
}

func (s *apiStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.record(r, body)
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/healthz":
		if s.healthDown {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, `{"status":"ok"}`)
	case r.Method == http.MethodPost && p == "/model":
		if s.failPublish {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "boom")
			return
		}
		writeJSON(w, http.StatusCreated, `{"version":{"id":"v1"}}`)
	case r.Method == http.MethodPost && p == "/policies":
		code := s.policyStatus
		if code == 0 {
			code = http.StatusCreated
		}
		w.WriteHeader(code)
	case r.Method == http.MethodPost && p == "/idp/connections":
		if s.connExists {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if s.badCreateJSON {
			writeJSON(w, http.StatusCreated, `{not json`)
			return
		}
		writeJSON(w, http.StatusCreated, `{"connection":{"id":"conn-1"}}`)
	case r.Method == http.MethodGet && p == "/idp/connections":
		if s.emptyList {
			writeJSON(w, http.StatusOK, `{"connections":[]}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"connections":[{"id":"conn-1","provider":"zitadel"}]}`)
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/idp/connections/"):
		writeJSON(w, http.StatusOK, `{"connection":{"id":"conn-1"}}`)
	case r.Method == http.MethodGet && strings.HasSuffix(p, "/rules"):
		if s.badRulesJSON {
			writeJSON(w, http.StatusOK, `{not json`)
			return
		}
		if s.existingRule != "" {
			writeJSON(w, http.StatusOK, `{"rules":[{"id":"`+s.existingRule+`"}]}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"rules":[]}`)
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/rules"):
		writeJSON(w, http.StatusCreated, `{"rule":{"id":"rule-1"}}`)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/idp/rules/"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/idp/connections/"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && strings.HasPrefix(p, "/policies/"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && p == "/idp/webhook/zitadel":
		if s.verifySig != nil && !s.verifySig(body, r.Header.Get("ZITADEL-Signature")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, `{"applied":1}`)
	case r.Method == http.MethodPost && p == "/access/v1/evaluation":
		if s.evalReason != "" {
			writeJSON(w, http.StatusOK, `{"decision":`+boolStr(s.evalDecision)+`,"context":{"reason":{"text":"`+s.evalReason+`"}}}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"decision":`+boolStr(s.evalDecision)+`}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// fakeTuples는 TupleGateway를 기록하는 fake다. writeErr/deleteErr로 오류 주입.
type fakeTuples struct {
	writes   []Tuple
	deletes  []Tuple
	writeErr error
}

func (f *fakeTuples) Write(_ context.Context, _ string, t Tuple) error {
	f.writes = append(f.writes, t)
	return f.writeErr
}

func (f *fakeTuples) Delete(_ context.Context, _ string, t Tuple) error {
	f.deletes = append(f.deletes, t)
	return nil
}

func newDeps(srv *httptest.Server, tuples TupleGateway, storeID func(context.Context) (string, error), out io.Writer) Deps {
	return Deps{
		APIBase:       srv.URL,
		AdminToken:    "devtoken",
		SigningSecret: "dev-zitadel-signing-secret",
		HTTP:          srv.Client(),
		StoreID:       storeID,
		Tuples:        tuples,
		Now:           func() time.Time { return time.Unix(1700000000, 0) },
		Out:           out,
	}
}

func constStore(id string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return id, nil }
}

func TestRun_happyPath(t *testing.T) {
	// 실 서명 엔진으로 webhook 서명 검증(데모가 401을 맞지 않음을 보장).
	spec := idp.WebhookSignatureSpec{
		Header: "ZITADEL-Signature", HeaderFormat: "kv_t_v", TimestampSource: "signature_header",
		TimestampUnit: "seconds", PayloadTemplate: "{timestamp}.{body}", Algorithm: "sha256",
		Encoding: "hex", SecretEncoding: "raw", ToleranceSec: 300, AllowMultipleSignatures: true,
	}
	stub := &apiStub{
		evalDecision: true,
		evalReason:   "user:alice is viewer via team:eng",
		verifySig: func(body []byte, sigHeader string) bool {
			h := http.Header{}
			h.Set("ZITADEL-Signature", sigHeader)
			return idp.VerifyWebhookSignature(spec, body, h, "dev-zitadel-signing-secret", func() time.Time { return time.Unix(1700000000, 0) })
		},
	}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tuples := &fakeTuples{}
	var out bytes.Buffer
	if err := Run(context.Background(), newDeps(srv, tuples, constStore("store-x"), &out)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 전 단계가 호출됐는지.
	for _, want := range [][2]string{
		{"POST", "/model"}, {"POST", "/policies"}, {"POST", "/idp/connections"},
		{"POST", "/idp/webhook/zitadel"}, {"POST", "/access/v1/evaluation"},
	} {
		if !stub.seen(want[0], want[1]) {
			t.Errorf("expected %s %s to be called", want[0], want[1])
		}
	}
	if len(tuples.writes) != 2 {
		t.Errorf("structural writes = %d, want 2", len(tuples.writes))
	}
	got := out.String()
	if !strings.Contains(got, "decision: ALLOW") {
		t.Errorf("output missing ALLOW decision:\n%s", got)
	}
	if !strings.Contains(got, "user:alice is viewer via team:eng") {
		t.Errorf("output missing reason text:\n%s", got)
	}
	if !strings.Contains(got, "✔ demo complete") {
		t.Errorf("output missing completion line:\n%s", got)
	}
}

func TestRun_connectionExists(t *testing.T) {
	// 연결이 이미 있으면 목록 조회 + PUT secret + 기존 규칙 삭제 경로를 탄다.
	stub := &apiStub{connExists: true, existingRule: "old-rule", evalDecision: false}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	var out bytes.Buffer
	if err := Run(context.Background(), newDeps(srv, &fakeTuples{}, constStore("store-x"), &out)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !stub.seen("GET", "/idp/connections") {
		t.Error("expected connection list lookup on existing connection")
	}
	if !stub.seen("PUT", "/idp/connections/conn-1") {
		t.Error("expected PUT to update the existing connection secret")
	}
	if !stub.seen("DELETE", "/idp/rules/old-rule") {
		t.Error("expected existing rule deletion (clear-then-insert)")
	}
	if !strings.Contains(out.String(), "decision: DENY") {
		t.Errorf("expected DENY output:\n%s", out.String())
	}
}

func TestRun_connectionConflictButMissingFromList(t *testing.T) {
	// POST가 409인데 목록엔 zitadel이 없는 모순 상태 → 명시적 오류.
	stub := &apiStub{connExists: true, emptyList: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	err := Run(context.Background(), newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard))
	if err == nil || !strings.Contains(err.Error(), "no existing zitadel connection") {
		t.Fatalf("err = %v, want no existing zitadel connection", err)
	}
}

func TestRun_malformedResponses(t *testing.T) {
	t.Run("connection create body", func(t *testing.T) {
		stub := &apiStub{badCreateJSON: true}
		srv := httptest.NewServer(stub)
		defer srv.Close()
		if err := Run(context.Background(), newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard)); err == nil {
			t.Fatal("expected error on malformed connection create response")
		}
	})
	t.Run("rules list body", func(t *testing.T) {
		stub := &apiStub{badRulesJSON: true}
		srv := httptest.NewServer(stub)
		defer srv.Close()
		if err := Run(context.Background(), newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard)); err == nil {
			t.Fatal("expected error on malformed rules list response")
		}
	})
}

func TestRun_idempotentTupleWriteSwallowed(t *testing.T) {
	stub := &apiStub{evalDecision: true, evalReason: "ok"}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tuples := &fakeTuples{writeErr: errors.New("cannot write a tuple which already exists")}
	var out bytes.Buffer
	if err := Run(context.Background(), newDeps(srv, tuples, constStore("store-x"), &out)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(out.String(), "structural tuple write failed") {
		t.Errorf("idempotent tuple error should be swallowed:\n%s", out.String())
	}
}

func TestRun_nonIdempotentTupleWriteWarns(t *testing.T) {
	stub := &apiStub{evalDecision: true, evalReason: "ok"}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tuples := &fakeTuples{writeErr: errors.New("connection refused")}
	var out bytes.Buffer
	if err := Run(context.Background(), newDeps(srv, tuples, constStore("store-x"), &out)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "structural tuple write failed") {
		t.Errorf("real tuple error should be surfaced as a warning:\n%s", out.String())
	}
}

func TestRun_apiNotReady(t *testing.T) {
	stub := &apiStub{healthDown: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	err := Run(context.Background(), newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard))
	if err == nil || !strings.Contains(err.Error(), "api not ready") {
		t.Fatalf("err = %v, want api not ready", err)
	}
}

func TestRun_publishFails(t *testing.T) {
	stub := &apiStub{failPublish: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	err := Run(context.Background(), newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard))
	if err == nil || !strings.Contains(err.Error(), "model publish failed") {
		t.Fatalf("err = %v, want model publish failed", err)
	}
}

func TestRun_storeNotBootstrapped(t *testing.T) {
	stub := &apiStub{evalDecision: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	err := Run(context.Background(), newDeps(srv, &fakeTuples{}, constStore(""), io.Discard))
	if err == nil || !strings.Contains(err.Error(), "instance_config missing") {
		t.Fatalf("err = %v, want instance_config missing", err)
	}
}

func TestRun_storeIDError(t *testing.T) {
	stub := &apiStub{evalDecision: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	failStore := func(context.Context) (string, error) { return "", errors.New("db down") }
	err := Run(context.Background(), newDeps(srv, &fakeTuples{}, failStore, io.Discard))
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("err = %v, want db down", err)
	}
}

func TestReset_full(t *testing.T) {
	stub := &apiStub{}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tuples := &fakeTuples{}
	var out bytes.Buffer
	if err := Reset(context.Background(), newDeps(srv, tuples, constStore("store-x"), &out)); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if !stub.seen("DELETE", "/policies/can-read-doc") {
		t.Error("expected policy deletion")
	}
	if !stub.seen("DELETE", "/idp/connections/conn-1") {
		t.Error("expected zitadel connection deletion")
	}
	if len(tuples.deletes) != 3 {
		t.Errorf("tuple deletes = %d, want 3", len(tuples.deletes))
	}
	if !strings.Contains(out.String(), "demo state reset") {
		t.Errorf("missing reset confirmation:\n%s", out.String())
	}
}

func TestReset_skipsTuplesWhenNoStore(t *testing.T) {
	stub := &apiStub{}
	srv := httptest.NewServer(stub)
	defer srv.Close()

	tuples := &fakeTuples{}
	if err := Reset(context.Background(), newDeps(srv, tuples, constStore(""), io.Discard)); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(tuples.deletes) != 0 {
		t.Errorf("no store id → tuple deletes should be skipped, got %d", len(tuples.deletes))
	}
}

// errDoer는 항상 전송 오류를 내는 Doer다(request 오류 경로 커버).
type errDoer struct{}

func (errDoer) Do(*http.Request) (*http.Response, error) { return nil, errors.New("transport down") }

// failAtDoer는 N번째 요청에서만 전송 오류를 내는 Doer다(각 단계의 오류 전파 경로 커버).
type failAtDoer struct {
	inner  Doer
	failOn int
	n      int
}

func (d *failAtDoer) Do(req *http.Request) (*http.Response, error) {
	d.n++
	if d.n == d.failOn {
		return nil, errors.New("injected transport failure")
	}
	return d.inner.Do(req)
}

// TestRun_failsAtEachStep은 데모의 각 HTTP 단계에서 전송 오류가 나면 Run이 오류를 반환하는지
// 확인한다(모든 request 오류 전파 분기 커버).
func TestRun_failsAtEachStep(t *testing.T) {
	stub := &apiStub{evalDecision: true, evalReason: "ok"}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	// 신규 연결 경로의 HTTP 요청 수(health, model, policies, conn-post, rules-get,
	// rules-post, webhook, eval) = 8.
	for step := 1; step <= 8; step++ {
		deps := newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard)
		deps.HTTP = &failAtDoer{inner: srv.Client(), failOn: step}
		if err := Run(context.Background(), deps); err == nil {
			t.Errorf("failOn=%d: Run should return an error when request %d fails", step, step)
		}
	}
}

// TestRun_failsAtExistingConnectionSteps는 연결이 이미 있는 경로의 목록조회/PUT 전송 오류를 커버한다.
func TestRun_failsAtExistingConnectionSteps(t *testing.T) {
	stub := &apiStub{connExists: true, evalDecision: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	// 요청 순서: 1 health,2 model,3 policies,4 conn-post(409),5 list,6 put,...
	for _, step := range []int{5, 6} {
		deps := newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard)
		deps.HTTP = &failAtDoer{inner: srv.Client(), failOn: step}
		if err := Run(context.Background(), deps); err == nil {
			t.Errorf("failOn=%d: Run should error on existing-connection transport failure", step)
		}
	}
}

// TestReset_failsAtEachStep은 Reset의 각 HTTP 단계 전송 오류 전파를 커버한다.
func TestReset_failsAtEachStep(t *testing.T) {
	stub := &apiStub{}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	// 요청 순서: 1 delete policy, 2 list connections, 3 delete connection.
	for step := 1; step <= 3; step++ {
		deps := newDeps(srv, &fakeTuples{}, constStore("store-x"), io.Discard)
		deps.HTTP = &failAtDoer{inner: srv.Client(), failOn: step}
		if err := Reset(context.Background(), deps); err == nil {
			t.Errorf("failOn=%d: Reset should return an error when request %d fails", step, step)
		}
	}
}

func TestRun_transportError(t *testing.T) {
	deps := Deps{
		APIBase: "http://example.invalid", AdminToken: "t", SigningSecret: "s",
		HTTP: errDoer{}, StoreID: constStore("store-x"), Tuples: &fakeTuples{}, Out: io.Discard,
	}
	err := Run(context.Background(), deps)
	if err == nil || !strings.Contains(err.Error(), "api not ready") {
		t.Fatalf("err = %v, want api not ready (transport failure at healthz)", err)
	}
}

func TestReset_transportError(t *testing.T) {
	deps := Deps{
		APIBase: "http://example.invalid", AdminToken: "t", SigningSecret: "s",
		HTTP: errDoer{}, StoreID: constStore("store-x"), Tuples: &fakeTuples{}, Out: io.Discard,
	}
	if err := Reset(context.Background(), deps); err == nil {
		t.Fatal("expected transport error to propagate from Reset")
	}
}

func TestRun_defaultsAppliedWithNilOut(t *testing.T) {
	// Out/Now 미주입이어도 패닉 없이 동작(withDefaults 커버).
	stub := &apiStub{evalDecision: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	deps := Deps{
		APIBase: srv.URL, AdminToken: "devtoken", SigningSecret: "s",
		HTTP: srv.Client(), StoreID: constStore("store-x"), Tuples: &fakeTuples{},
	}
	if err := Run(context.Background(), deps); err != nil {
		t.Fatalf("Run with defaults: %v", err)
	}
}
