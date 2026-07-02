package httpx

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// NewRouter는 공통 미들웨어(요청 로깅 → 패닉 복구)와 /healthz를 붙인 chi 라우터를 만든다.
// 비즈니스 모듈 마운트(/model, /tokens 등)는 LFGA-25/26이 추가한다.
// 미매칭 라우트/메서드는 Hono 기본과 동일한 본문("404 Not Found", text/plain)을 돌려준다 —
// LFGA-27 이중 백엔드 contract replay에서 프레임워크 기본값 차이를 없애기 위함.
func NewRouter(logger *slog.Logger, health Health) *chi.Mux {
	r := chi.NewRouter()
	r.Use(RequestLogger(logger))
	r.Use(Recoverer(logger))
	r.NotFound(WriteHonoNotFound)
	r.MethodNotAllowed(WriteHonoNotFound)
	r.Get("/healthz", health.Handler())
	return r
}

// WriteHonoNotFound는 Hono 기본 notFound 응답("404 Not Found", text/plain)을 재현한다.
func WriteHonoNotFound(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 Not Found"))
}

// WriteHonoInternalError는 Hono 기본 onError 응답("Internal Server Error", text/plain)을 재현한다.
// TS 백엔드에서 미처리 throw(인프라 장애·패닉 상당)는 이 형태로 나갔다.
func WriteHonoInternalError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte("Internal Server Error"))
}
