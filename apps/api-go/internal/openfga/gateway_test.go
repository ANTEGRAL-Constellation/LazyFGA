package openfga

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fga "github.com/openfga/go-sdk"
	fgaclient "github.com/openfga/go-sdk/client"
)

const (
	storeULID   = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	otherULID   = "01BX5ZZKBKACTAV9WEVGEMMVRZ"
	createdULID = "01HZ0000000000000000000000"
	modelULID   = "01HZ000000000000000000000M"
)

// fakeFGA는 OpenFGA HTTP API의 최소 모의 서버다. 각 엔드포인트 동작을 필드로 조정한다.
type fakeFGA struct {
	getStoreStatus    int // GET /stores/{id}
	createStoreStatus int
	listStoresStatus  int
	checkStatus       int
	checkAllowed      bool
	writeStatus       int
	authModelStatus   int
	readPages         []string // 순차 반환할 read 응답 JSON body 목록

	getStoreCalls    int
	createStoreCalls int
	listStoresCalls  int
	writeCalls       int
	readCalls        int
	lastWriteBody    string
	lastReadBody     string
}

func newFakeFGA() *fakeFGA {
	return &fakeFGA{
		getStoreStatus:    http.StatusOK,
		createStoreStatus: http.StatusOK,
		listStoresStatus:  http.StatusOK,
		checkStatus:       http.StatusOK,
		checkAllowed:      true,
		writeStatus:       http.StatusOK,
		authModelStatus:   http.StatusOK,
	}
}

func (f *fakeFGA) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// validationBody는 400 검증 오류 응답(재시도 유발하지 않음)이다.
func validationBody(w http.ResponseWriter) {
	writeJSON(w, http.StatusBadRequest, `{"code":"validation_error","message":"bad request"}`)
}

func (f *fakeFGA) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/stores":
		f.listStoresCalls++
		if f.listStoresStatus != http.StatusOK {
			validationBody(w)
			return
		}
		writeJSON(w, http.StatusOK, `{"stores":[],"continuation_token":""}`)
	case r.Method == http.MethodPost && path == "/stores":
		f.createStoreCalls++
		if f.createStoreStatus != http.StatusOK {
			validationBody(w)
			return
		}
		writeJSON(w, http.StatusOK, `{"id":"`+createdULID+`","name":"lazyfga","created_at":"2026-07-02T00:00:00Z","updated_at":"2026-07-02T00:00:00Z"}`)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/stores/"):
		f.getStoreCalls++
		if f.getStoreStatus != http.StatusOK {
			w.WriteHeader(f.getStoreStatus)
			_, _ = io.WriteString(w, `{"code":"store_id_not_found","message":"not found"}`)
			return
		}
		id := strings.TrimPrefix(path, "/stores/")
		writeJSON(w, http.StatusOK, `{"id":"`+id+`","name":"lazyfga","created_at":"2026-07-02T00:00:00Z","updated_at":"2026-07-02T00:00:00Z"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/check"):
		if f.checkStatus != http.StatusOK {
			validationBody(w)
			return
		}
		allowed := "false"
		if f.checkAllowed {
			allowed = "true"
		}
		writeJSON(w, http.StatusOK, `{"allowed":`+allowed+`}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/read"):
		b, _ := io.ReadAll(r.Body)
		f.lastReadBody = string(b)
		idx := f.readCalls
		f.readCalls++
		if len(f.readPages) == 0 {
			writeJSON(w, http.StatusOK, `{"tuples":[],"continuation_token":""}`)
			return
		}
		if idx >= len(f.readPages) {
			idx = len(f.readPages) - 1
		}
		writeJSON(w, http.StatusOK, f.readPages[idx])
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/write"):
		b, _ := io.ReadAll(r.Body)
		f.lastWriteBody = string(b)
		f.writeCalls++
		if f.writeStatus != http.StatusOK {
			writeJSON(w, http.StatusBadRequest, `{"code":"write_failed_due_to_invalid_input","message":"tuple already exists"}`)
			return
		}
		writeJSON(w, http.StatusOK, `{}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/authorization-models"):
		if f.authModelStatus != http.StatusOK {
			validationBody(w)
			return
		}
		writeJSON(w, http.StatusOK, `{"authorization_model_id":"`+modelULID+`"}`)
	default:
		http.Error(w, "unexpected path: "+r.Method+" "+path, http.StatusNotImplemented)
	}
}

func newGatewayForTest(t *testing.T, f *fakeFGA) *gatewayImpl {
	t.Helper()
	srv := f.server(t)
	g, err := NewGateway(srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return g.(*gatewayImpl)
}

// noopPersist/noopLoad은 콜백 기본값이다.
func noopPersist(context.Context, string) error { return nil }

func TestNewGateway_invalidURL(t *testing.T) {
	if _, err := NewGateway("://bad-url", nil); err == nil {
		t.Fatal("expected error for invalid apiURL")
	}
}

func TestBootstrap_envStoreExists(t *testing.T) {
	f := newFakeFGA()
	g := newGatewayForTest(t, f)
	var persisted string
	id, err := g.Bootstrap(context.Background(), BootstrapOptions{
		EnvStoreID:        storeULID,
		LoadStoredStoreID: func(context.Context) (string, error) { return "", errors.New("must not be called") },
		PersistStoreID:    func(_ context.Context, s string) error { persisted = s; return nil },
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if id != storeULID {
		t.Errorf("storeID = %q, want %q", id, storeULID)
	}
	if persisted != storeULID {
		t.Errorf("persisted = %q, want %q", persisted, storeULID)
	}
	if f.createStoreCalls != 0 {
		t.Errorf("createStoreCalls = %d, want 0 (should reuse existing)", f.createStoreCalls)
	}
	if f.getStoreCalls != 1 {
		t.Errorf("getStoreCalls = %d, want 1", f.getStoreCalls)
	}
	if got, _ := g.StoreID(); got != storeULID {
		t.Errorf("StoreID() = %q, want %q", got, storeULID)
	}
}

func TestBootstrap_envStoreMissing_createsAndWarns(t *testing.T) {
	f := newFakeFGA()
	f.getStoreStatus = http.StatusNotFound
	g := newGatewayForTest(t, f)
	var persisted string
	id, err := g.Bootstrap(context.Background(), BootstrapOptions{
		EnvStoreID:        otherULID,
		LoadStoredStoreID: noopLoad,
		PersistStoreID:    func(_ context.Context, s string) error { persisted = s; return nil },
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if id != createdULID {
		t.Errorf("storeID = %q, want created %q", id, createdULID)
	}
	if persisted != createdULID {
		t.Errorf("persisted = %q, want %q", persisted, createdULID)
	}
	if f.createStoreCalls != 1 {
		t.Errorf("createStoreCalls = %d, want 1", f.createStoreCalls)
	}
}

func TestBootstrap_noEnv_loadedStoreExists(t *testing.T) {
	f := newFakeFGA()
	g := newGatewayForTest(t, f)
	id, err := g.Bootstrap(context.Background(), BootstrapOptions{
		LoadStoredStoreID: func(context.Context) (string, error) { return storeULID, nil },
		PersistStoreID:    noopPersist,
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if id != storeULID {
		t.Errorf("storeID = %q, want %q", id, storeULID)
	}
	if f.createStoreCalls != 0 {
		t.Errorf("createStoreCalls = %d, want 0", f.createStoreCalls)
	}
}

func TestBootstrap_noEnv_noStored_creates(t *testing.T) {
	f := newFakeFGA()
	g := newGatewayForTest(t, f)
	id, err := g.Bootstrap(context.Background(), BootstrapOptions{
		LoadStoredStoreID: func(context.Context) (string, error) { return "", nil },
		PersistStoreID:    noopPersist,
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if id != createdULID {
		t.Errorf("storeID = %q, want %q", id, createdULID)
	}
	if f.getStoreCalls != 0 {
		t.Errorf("getStoreCalls = %d, want 0 (no candidate)", f.getStoreCalls)
	}
	if f.createStoreCalls != 1 {
		t.Errorf("createStoreCalls = %d, want 1", f.createStoreCalls)
	}
}

func TestBootstrap_loadError(t *testing.T) {
	f := newFakeFGA()
	g := newGatewayForTest(t, f)
	_, err := g.Bootstrap(context.Background(), BootstrapOptions{
		LoadStoredStoreID: func(context.Context) (string, error) { return "", errors.New("db down") },
		PersistStoreID:    noopPersist,
	})
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("expected load error, got %v", err)
	}
}

func TestBootstrap_persistError(t *testing.T) {
	f := newFakeFGA()
	g := newGatewayForTest(t, f)
	_, err := g.Bootstrap(context.Background(), BootstrapOptions{
		LoadStoredStoreID: func(context.Context) (string, error) { return "", nil },
		PersistStoreID:    func(context.Context, string) error { return errors.New("persist fail") },
	})
	if err == nil || !strings.Contains(err.Error(), "persist fail") {
		t.Fatalf("expected persist error, got %v", err)
	}
}

func TestBootstrap_createStoreError(t *testing.T) {
	f := newFakeFGA()
	f.createStoreStatus = http.StatusBadRequest
	g := newGatewayForTest(t, f)
	_, err := g.Bootstrap(context.Background(), BootstrapOptions{
		LoadStoredStoreID: func(context.Context) (string, error) { return "", nil },
		PersistStoreID:    noopPersist,
	})
	if err == nil {
		t.Fatal("expected create store error")
	}
}

func TestBootstrap_storeClientError(t *testing.T) {
	f := newFakeFGA()
	g := newGatewayForTest(t, f)
	g.newStoreClient = func(string, string) (*fgaclient.OpenFgaClient, error) {
		return nil, errors.New("bind fail")
	}
	_, err := g.Bootstrap(context.Background(), BootstrapOptions{
		LoadStoredStoreID: func(context.Context) (string, error) { return "", nil },
		PersistStoreID:    noopPersist,
	})
	if err == nil || !strings.Contains(err.Error(), "bind fail") {
		t.Fatalf("expected bind error, got %v", err)
	}
}

func TestStoreID_beforeBootstrap(t *testing.T) {
	f := newFakeFGA()
	g := newGatewayForTest(t, f)
	if _, err := g.StoreID(); err == nil {
		t.Fatal("expected error before bootstrap")
	}
}

func TestPing(t *testing.T) {
	t.Run("up", func(t *testing.T) {
		g := newGatewayForTest(t, newFakeFGA())
		if !g.Ping(context.Background()) {
			t.Fatal("expected ping true")
		}
	})
	t.Run("down", func(t *testing.T) {
		f := newFakeFGA()
		f.listStoresStatus = http.StatusBadRequest
		g := newGatewayForTest(t, f)
		if g.Ping(context.Background()) {
			t.Fatal("expected ping false")
		}
	})
}

func bootstrapped(t *testing.T, f *fakeFGA) *gatewayImpl {
	t.Helper()
	g := newGatewayForTest(t, f)
	if _, err := g.Bootstrap(context.Background(), BootstrapOptions{
		EnvStoreID:        storeULID,
		LoadStoredStoreID: noopLoad,
		PersistStoreID:    noopPersist,
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return g
}

func noopLoad(context.Context) (string, error) { return "", nil }

func TestCheck(t *testing.T) {
	t.Run("not bootstrapped", func(t *testing.T) {
		g := newGatewayForTest(t, newFakeFGA())
		if _, err := g.Check(context.Background(), CheckInput{User: "user:a", Relation: "viewer", Object: "doc:1"}); err == nil {
			t.Fatal("expected error before bootstrap")
		}
	})
	t.Run("allowed with context and model pin", func(t *testing.T) {
		f := newFakeFGA()
		f.checkAllowed = true
		g := bootstrapped(t, f)
		allowed, err := g.Check(context.Background(),
			CheckInput{User: "user:a", Relation: "viewer", Object: "doc:1", Context: map[string]any{"tz": "UTC"}},
			WithCheckAuthorizationModelID(modelULID))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if !allowed {
			t.Fatal("expected allowed")
		}
	})
	t.Run("denied", func(t *testing.T) {
		f := newFakeFGA()
		f.checkAllowed = false
		g := bootstrapped(t, f)
		allowed, err := g.Check(context.Background(), CheckInput{User: "user:a", Relation: "viewer", Object: "doc:1"})
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		if allowed {
			t.Fatal("expected denied")
		}
	})
	t.Run("error", func(t *testing.T) {
		f := newFakeFGA()
		f.checkStatus = http.StatusBadRequest
		g := bootstrapped(t, f)
		if _, err := g.Check(context.Background(), CheckInput{User: "user:a", Relation: "viewer", Object: "doc:1"}); err == nil {
			t.Fatal("expected check error")
		}
	})
}

func TestRead_pagination(t *testing.T) {
	f := newFakeFGA()
	f.readPages = []string{
		`{"tuples":[{"key":{"user":"user:a","relation":"viewer","object":"doc:1","condition":{"name":"in_hours","context":{"tz":"UTC"}}},"timestamp":"2026-07-02T00:00:00Z"}],"continuation_token":"next"}`,
		`{"tuples":[{"key":{"user":"user:b","relation":"editor","object":"doc:2"},"timestamp":"2026-07-02T00:00:00Z"}],"continuation_token":""}`,
	}
	g := bootstrapped(t, f)
	tuples, err := g.Read(context.Background(), ReadInput{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(tuples) != 2 {
		t.Fatalf("collected %d tuples across pages, want 2", len(tuples))
	}
	if f.readCalls != 2 {
		t.Errorf("readCalls = %d, want 2 (must follow continuation token)", f.readCalls)
	}
	if tuples[0].Condition == nil || tuples[0].Condition.Name != "in_hours" || tuples[0].Condition.Context["tz"] != "UTC" {
		t.Errorf("condition passthrough failed: %+v", tuples[0].Condition)
	}
	if tuples[1].Condition != nil {
		t.Errorf("expected no condition on tuple 2, got %+v", tuples[1].Condition)
	}
}

func TestRead_filterAndErrors(t *testing.T) {
	t.Run("not bootstrapped", func(t *testing.T) {
		g := newGatewayForTest(t, newFakeFGA())
		if _, err := g.Read(context.Background(), ReadInput{}); err == nil {
			t.Fatal("expected error before bootstrap")
		}
	})
	t.Run("empty result with filter", func(t *testing.T) {
		f := newFakeFGA()
		g := bootstrapped(t, f)
		obj := "doc:1"
		tuples, err := g.Read(context.Background(), ReadInput{Object: &obj})
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(tuples) != 0 {
			t.Fatalf("want 0 tuples, got %d", len(tuples))
		}
		if !strings.Contains(f.lastReadBody, "doc:1") {
			t.Errorf("filter not forwarded, body=%s", f.lastReadBody)
		}
	})
}

func TestWrite(t *testing.T) {
	t.Run("not bootstrapped", func(t *testing.T) {
		g := newGatewayForTest(t, newFakeFGA())
		err := g.Write(context.Background(), WriteInput{Writes: []fga.TupleKey{{User: "user:a", Relation: "viewer", Object: "doc:1"}}})
		if err == nil {
			t.Fatal("expected error before bootstrap")
		}
	})
	t.Run("success with model pin", func(t *testing.T) {
		f := newFakeFGA()
		g := bootstrapped(t, f)
		err := g.Write(context.Background(),
			WriteInput{Writes: []fga.TupleKey{{User: "user:a", Relation: "viewer", Object: "doc:1"}}},
			WithWriteAuthorizationModelID(modelULID))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if f.writeCalls != 1 {
			t.Errorf("writeCalls = %d, want 1", f.writeCalls)
		}
		// SDK가 실제로 보내는 body를 핀 고정: tuple_keys + 모델 핀. go-sdk v0.8.2는 옵션이
		// 있으면 on_duplicate:""를 함께 보낸다(OpenFGA proto상 "" = 기본 동작이라 무해) — 그
		// 사실이 바뀌면(동작 변화 가능성) 여기서 잡히도록 전체 body를 검증한다.
		var body struct {
			AuthorizationModelID string `json:"authorization_model_id"`
			Writes               struct {
				TupleKeys []struct {
					User     string `json:"user"`
					Relation string `json:"relation"`
					Object   string `json:"object"`
				} `json:"tuple_keys"`
				OnDuplicate string `json:"on_duplicate"`
			} `json:"writes"`
		}
		if err := json.Unmarshal([]byte(f.lastWriteBody), &body); err != nil {
			t.Fatalf("write body not JSON: %v (%s)", err, f.lastWriteBody)
		}
		if body.AuthorizationModelID != modelULID {
			t.Errorf("authorization_model_id = %q, want %q", body.AuthorizationModelID, modelULID)
		}
		if len(body.Writes.TupleKeys) != 1 || body.Writes.TupleKeys[0].User != "user:a" ||
			body.Writes.TupleKeys[0].Relation != "viewer" || body.Writes.TupleKeys[0].Object != "doc:1" {
			t.Errorf("unexpected tuple_keys in write body: %s", f.lastWriteBody)
		}
		if body.Writes.OnDuplicate != "" {
			t.Errorf("on_duplicate = %q, want empty (default error-on-duplicate behavior)", body.Writes.OnDuplicate)
		}
	})
	t.Run("delete", func(t *testing.T) {
		f := newFakeFGA()
		g := bootstrapped(t, f)
		err := g.Write(context.Background(),
			WriteInput{Deletes: []fga.TupleKeyWithoutCondition{{User: "user:a", Relation: "viewer", Object: "doc:1"}}})
		if err != nil {
			t.Fatalf("Write(delete): %v", err)
		}
	})
	t.Run("error surfaces raw for classification", func(t *testing.T) {
		f := newFakeFGA()
		f.writeStatus = http.StatusBadRequest
		g := bootstrapped(t, f)
		err := g.Write(context.Background(), WriteInput{Writes: []fga.TupleKey{{User: "user:a", Relation: "viewer", Object: "doc:1"}}})
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			t.Fatalf("expected raw invalid-input error, got %v", err)
		}
	})
}

func TestWriteAuthorizationModel(t *testing.T) {
	t.Run("not bootstrapped", func(t *testing.T) {
		g := newGatewayForTest(t, newFakeFGA())
		if _, err := g.WriteAuthorizationModel(context.Background(), fga.WriteAuthorizationModelRequest{}); err == nil {
			t.Fatal("expected error before bootstrap")
		}
	})
	t.Run("success", func(t *testing.T) {
		f := newFakeFGA()
		g := bootstrapped(t, f)
		id, err := g.WriteAuthorizationModel(context.Background(), fga.WriteAuthorizationModelRequest{
			SchemaVersion: "1.1",
			TypeDefinitions: []fga.TypeDefinition{
				{Type: "user"},
			},
		})
		if err != nil {
			t.Fatalf("WriteAuthorizationModel: %v", err)
		}
		if id != modelULID {
			t.Errorf("modelID = %q, want %q", id, modelULID)
		}
	})
	t.Run("error", func(t *testing.T) {
		f := newFakeFGA()
		f.authModelStatus = http.StatusBadRequest
		g := bootstrapped(t, f)
		if _, err := g.WriteAuthorizationModel(context.Background(), fga.WriteAuthorizationModelRequest{}); err == nil {
			t.Fatal("expected error")
		}
	})
}
