package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/antegral-constellation/lazyfga/api/internal/config"
)

// httpDoer는 healthcheck가 필요로 하는 HTTP 클라이언트 인터페이스다(테스트 주입 지점).
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Healthcheck는 자기 자신의 /healthz를 GET해 200이면 0, 아니면 1을 반환한다.
// 컨테이너 healthcheck용 바이너리 모드(LFGA-27 spec)의 실행 본체다.
func Healthcheck(ctx context.Context) int {
	cfg, err := config.Load()
	if err != nil {
		return 1
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Port)
	return healthcheckStatus(ctx, http.DefaultClient, url)
}

func healthcheckStatus(ctx context.Context, client httpDoer, url string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 1
	}
	resp, err := client.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		return 0
	}
	return 1
}
