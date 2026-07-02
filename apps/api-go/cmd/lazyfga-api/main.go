// Command lazyfga-api는 lazyFGA control-plane 서버 진입점이다.
// 인자 없이 실행하면 서버를, `healthcheck` 인자로 실행하면 자기 /healthz를 확인하고 종료한다.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/antegral-constellation/lazyfga/api/internal/app"
)

// osExit는 테스트에서 대체 가능한 종료 함수다.
var osExit = os.Exit

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	osExit(run(ctx, os.Args))
}

// run은 인자를 해석해 서버 또는 healthcheck를 실행하고 종료코드를 반환한다.
func run(ctx context.Context, args []string) int {
	if len(args) > 1 && args[1] == "healthcheck" {
		return app.Healthcheck(ctx)
	}
	if err := app.Run(ctx); err != nil {
		return 1
	}
	return 0
}
