package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// statusRecorder는 응답 상태코드를 기록한다(요청 로깅용).
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.written {
		r.status = http.StatusOK
		r.written = true
	}
	return r.ResponseWriter.Write(b)
}

// RequestLogger는 각 요청의 method·path·status·소요시간을 slog로 남긴다.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// Recoverer는 핸들러 패닉을 복구해 500을 반환하고 스택을 로깅한다.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered",
						"err", rec,
						"path", r.URL.Path,
						"stack", string(debug.Stack()),
					)
					// TS 미처리 throw는 Hono 기본 onError 본문으로 나갔다 — 동일 재현.
					WriteHonoInternalError(w)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit은 요청 본문 크기를 제한한다. Content-Length 초과 시 즉시 413을 반환하고,
// 길이 불명(chunked) 요청은 MaxBytesReader로 감싸 핸들러의 본문 읽기 단계에서 막는다.
// LFGA-26 webhook 라우트가 핸들러 로직 이전에 이를 적용한다.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				WriteError(w, http.StatusRequestEntityTooLarge, "payload too large")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
