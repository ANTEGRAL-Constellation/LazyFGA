package writeerror

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"

	fga "github.com/openfga/go-sdk"
)

// apiResp는 SDK 오류 생성자가 요구하는 최소 *http.Response를 만든다.
func apiResp(status int) *http.Response {
	req, _ := http.NewRequest(http.MethodPost, "http://openfga.local/stores/s/write", nil)
	return &http.Response{StatusCode: status, Header: make(http.Header), Request: req}
}

// timeoutNetErr은 net.Error를 구현하지만 url/op 타입이 아닌 오류다.
type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

func validationErr(body string) error {
	return fga.NewFgaApiValidationError("Write", nil, apiResp(http.StatusBadRequest), []byte(body), "s")
}

func TestIsTransientAPIError_httpStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"internal 500", fga.NewFgaApiInternalError("Write", nil, apiResp(500), nil, "s"), true},
		{"internal 501", fga.NewFgaApiInternalError("Write", nil, apiResp(501), nil, "s"), true},
		{"rate limit 429", fga.NewFgaApiRateLimitExceededError("Write", nil, apiResp(429), nil, "s"), true},
		{"generic 502", fga.NewFgaApiError("Write", nil, apiResp(502), nil, "s"), true},
		{"validation 400", validationErr(`{"code":"x"}`), false},
		{"not found 404", fga.NewFgaApiNotFoundError("Write", nil, apiResp(404), nil, "s"), false},
		{"auth 403", fga.NewFgaApiAuthenticationError("Write", nil, apiResp(403), nil, "s"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransientAPIError(tc.err); got != tc.want {
				t.Fatalf("IsTransientAPIError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsTransientAPIError_networkAndMessage(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"url.Error wrapping op error", &url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, true},
		{"bare net.OpError", &net.OpError{Op: "dial", Err: syscall.ECONNRESET}, true},
		{"dns error", &net.DNSError{Err: "no such host", Name: "fga"}, true},
		{"bare syscall errno", syscall.ECONNREFUSED, true},
		{"net.Error timeout impl", timeoutNetErr{}, true},
		{"message fetch failed", errors.New("fetch failed"), true},
		{"message network", errors.New("some NETWORK glitch"), true},
		{"message timeout", errors.New("operation Timeout"), true},
		{"message econnrefused", errors.New("dial tcp: ECONNREFUSED"), true},
		{"unknown status-less non-transient", errors.New("boom unspecified"), false},
		{"decode error is non-transient", fga.GenericOpenAPIError{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransientAPIError(tc.err); got != tc.want {
				t.Fatalf("IsTransientAPIError = %v, want %v", got, tc.want)
			}
		})
	}
}

// syscall.Errno가 net.Error를 구현함을 컴파일 타임에 고정한다(단일 net.Error 검사의 근거).
var _ net.Error = syscall.Errno(0)

func TestClassifyWriteError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		op             Op
		wantIdempotent bool
		wantTransient  bool
	}{
		{
			name:          "transient 500 short-circuits",
			err:           fga.NewFgaApiInternalError("Write", nil, apiResp(500), nil, "s"),
			op:            OpWrite,
			wantTransient: true,
		},
		{
			name:           "write duplicate absorbed via message",
			err:            validationErr(`{"code":"write_failed_due_to_invalid_input","message":"tuple already exists"}`),
			op:             OpWrite,
			wantIdempotent: true,
		},
		{
			name:           "write duplicate via 'duplicate' keyword",
			err:            errors.New("write_failed_due_to_invalid_input: duplicate tuple"),
			op:             OpWrite,
			wantIdempotent: true,
		},
		{
			name:           "write invalid-input but no dup pattern is not absorbed",
			err:            errors.New("write_failed_due_to_invalid_input: relation not found"),
			op:             OpWrite,
			wantIdempotent: false,
		},
		{
			name:           "delete missing absorbed via 'does not exist'",
			err:            errors.New("write_failed_due_to_invalid_input: tuple does not exist"),
			op:             OpDelete,
			wantIdempotent: true,
		},
		{
			name:           "delete 'cannot delete' absorbed",
			err:            errors.New("write_failed_due_to_invalid_input: cannot delete a missing tuple"),
			op:             OpDelete,
			wantIdempotent: true,
		},
		{
			name:           "delete vague 'not found' is NOT absorbed (over-match guard)",
			err:            errors.New("write_failed_due_to_invalid_input: type not found"),
			op:             OpDelete,
			wantIdempotent: false,
		},
		{
			name:           "dup pattern without invalid-input signal is not absorbed",
			err:            errors.New("tuple already exists"),
			op:             OpWrite,
			wantIdempotent: false,
		},
		{
			name:           "plain deterministic error",
			err:            errors.New("bad request"),
			op:             OpWrite,
			wantIdempotent: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyWriteError(tc.err, tc.op)
			if got.Idempotent != tc.wantIdempotent {
				t.Errorf("Idempotent = %v, want %v", got.Idempotent, tc.wantIdempotent)
			}
			if got.Transient != tc.wantTransient {
				t.Errorf("Transient = %v, want %v", got.Transient, tc.wantTransient)
			}
		})
	}
}

func TestResponseCode_validationErrorBranch(t *testing.T) {
	// 생성자만으로는 responseCode가 비어 있으나 errors.As 분기 자체를 실행한다.
	if got := responseCode(validationErr(`{}`)); got != "" {
		t.Fatalf("responseCode = %q, want empty (constructor leaves code unset)", got)
	}
	// 비검증 오류는 빈 문자열.
	if got := responseCode(errors.New("x")); got != "" {
		t.Fatalf("responseCode(non-validation) = %q, want empty", got)
	}
}

func TestErrMessage_nil(t *testing.T) {
	if got := errMessage(nil); got != "" {
		t.Fatalf("errMessage(nil) = %q, want empty", got)
	}
}
