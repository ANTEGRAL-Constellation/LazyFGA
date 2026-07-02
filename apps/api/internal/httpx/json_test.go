package httpx

import (
	"net/http/httptest"
	"testing"
)

func TestWriteJSON_success(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, 201, map[string]any{"a": 1, "b": "two"})
	if w.Code != 201 {
		t.Errorf("status = %d, want 201", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	// 후행 개행이 없어야 한다.
	body := w.Body.String()
	if body != `{"a":1,"b":"two"}` {
		t.Errorf("body = %q", body)
	}
}

func TestWriteJSON_marshalError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSON(w, 200, make(chan int)) // 직렬화 불가.
	if w.Code != 500 {
		t.Errorf("status = %d, want 500", w.Code)
	}
	if w.Body.String() != `{"error":"internal error"}` {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, 403, "forbidden")
	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if w.Body.String() != `{"error":"forbidden"}` {
		t.Errorf("body = %q", w.Body.String())
	}
}
