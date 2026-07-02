package audit

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/jackc/pgx/v5/pgconn"
)

type execCall struct {
	sql  string
	args []any
}

type fakeExecer struct {
	mu       sync.Mutex
	calls    []execCall
	err      error
	doPanic  bool
	signalCh chan struct{}
}

func (f *fakeExecer) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if f.doPanic {
		panic("exec boom")
	}
	f.mu.Lock()
	f.calls = append(f.calls, execCall{sql: sql, args: args})
	f.mu.Unlock()
	if f.signalCh != nil {
		close(f.signalCh)
	}
	return pgconn.CommandTag{}, f.err
}

func (f *fakeExecer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// syncRecorder는 디스패치를 동기화해 삽입을 결정적으로 관찰한다.
func syncRecorder(db execer, logger *slog.Logger) *DBRecorder {
	r := NewDBRecorder(db, logger)
	r.dispatch = func(fn func()) { fn() }
	return r
}

func TestRecord_insertsWithDefaults(t *testing.T) {
	fe := &fakeExecer{}
	logger, _ := bufLogger()
	r := syncRecorder(fe, logger)

	r.Record("model.publish", nil, "") // data nil, actor 빈 문자열.
	if fe.callCount() != 1 {
		t.Fatalf("exec calls = %d, want 1", fe.callCount())
	}
	call := fe.calls[0]
	if !strings.Contains(call.sql, "INSERT INTO audit_log") {
		t.Errorf("unexpected sql: %s", call.sql)
	}
	if call.args[0] != "model.publish" {
		t.Errorf("action arg = %v", call.args[0])
	}
	if call.args[1] != "{}" {
		t.Errorf("data arg = %v, want {}", call.args[1])
	}
	if call.args[2] != "system" {
		t.Errorf("actor arg = %v, want system", call.args[2])
	}
}

func TestRecord_marshalsData(t *testing.T) {
	fe := &fakeExecer{}
	logger, _ := bufLogger()
	r := syncRecorder(fe, logger)

	r.Record("grant.write", map[string]any{"user": "u1"}, "admin")
	if fe.callCount() != 1 {
		t.Fatalf("exec calls = %d, want 1", fe.callCount())
	}
	if fe.calls[0].args[1] != `{"user":"u1"}` {
		t.Errorf("data arg = %v", fe.calls[0].args[1])
	}
	if fe.calls[0].args[2] != "admin" {
		t.Errorf("actor arg = %v", fe.calls[0].args[2])
	}
}

func TestRecord_execErrorIsLoggedNotFatal(t *testing.T) {
	fe := &fakeExecer{err: context.DeadlineExceeded}
	logger, buf := bufLogger()
	r := syncRecorder(fe, logger)

	r.Record("x", nil, "") // 패닉/블록 없이 반환해야 한다.
	if !strings.Contains(buf.String(), "audit insert failed") {
		t.Errorf("insert error not logged: %s", buf.String())
	}
}

func TestRecord_marshalErrorIsLogged(t *testing.T) {
	fe := &fakeExecer{}
	logger, buf := bufLogger()
	r := syncRecorder(fe, logger)

	r.Record("x", map[string]any{"bad": make(chan int)}, "") // 직렬화 불가.
	if fe.callCount() != 0 {
		t.Error("exec must not run when marshal fails")
	}
	if !strings.Contains(buf.String(), "audit marshal failed") {
		t.Errorf("marshal error not logged: %s", buf.String())
	}
}

func TestRecord_execPanicRecovered(t *testing.T) {
	fe := &fakeExecer{doPanic: true}
	logger, buf := bufLogger()
	r := syncRecorder(fe, logger)

	r.Record("x", nil, "") // 패닉이 전파되면 이 테스트는 크래시한다.
	if !strings.Contains(buf.String(), "audit insert panicked") {
		t.Errorf("insert panic not logged: %s", buf.String())
	}
}

func TestRecord_dispatchPanicRecovered(t *testing.T) {
	logger, buf := bufLogger()
	r := NewDBRecorder(&fakeExecer{}, logger)
	r.dispatch = func(func()) { panic("dispatch boom") } // 디스패치 자체가 패닉.

	r.Record("x", nil, "") // 바깥 recover가 흡수해야 한다.
	if !strings.Contains(buf.String(), "audit record panicked") {
		t.Errorf("outer panic not logged: %s", buf.String())
	}
}

func TestRecord_defaultAsyncDispatch(t *testing.T) {
	fe := &fakeExecer{signalCh: make(chan struct{})}
	logger, _ := bufLogger()
	r := NewDBRecorder(fe, logger) // 기본 goroutine 디스패치.

	r.Record("async", nil, "")
	select {
	case <-fe.signalCh:
	case <-time.After(2 * time.Second):
		t.Fatal("async insert did not run")
	}
}

func TestRecord_nilLoggerDefaults(t *testing.T) {
	// logger nil이면 slog.Default로 대체되어 패닉 없이 동작.
	r := syncRecorder(&fakeExecer{}, nil)
	if r.logger == nil {
		t.Fatal("logger should default")
	}
	r.Record("x", nil, "")
}

func TestPrincipalActor(t *testing.T) {
	tests := []struct {
		name string
		p    httpx.Principal
		want string
	}{
		{"admin", httpx.Principal{Role: httpx.RoleAdmin}, "admin"},
		{"service with token", httpx.Principal{Role: httpx.RoleService, TokenID: "tok-9"}, "service:tok-9"},
		{"service without token", httpx.Principal{Role: httpx.RoleService}, "service"},
	}
	for _, tc := range tests {
		if got := PrincipalActor(tc.p); got != tc.want {
			t.Errorf("%s: PrincipalActor = %q, want %q", tc.name, got, tc.want)
		}
	}
}
