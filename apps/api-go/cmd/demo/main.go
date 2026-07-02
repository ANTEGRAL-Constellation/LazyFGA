// Command demo는 lazyfga-19 자체 완결 데모 오케스트레이터 CLI다(run | reset).
// 로직은 internal/democli에 있고 여기서는 프로덕션 어댑터(pgx·go-sdk·http)를 조립만 한다.
//
// 실행(api·openfga·postgres 기동 상태에서):
//
//	go run ./cmd/demo run     # 발행 → 정책 → IdP → webhook → 구조 tuple → evaluate
//	go run ./cmd/demo reset   # 정책/IdP/데모 tuple 정리
//
// 환경변수(TS 스크립트와 동일 계약):
//
//	API_BASE(=http://localhost:8787) ADMIN_TOKEN(=devtoken)
//	ZITADEL_SIGNING_SECRET(=dev-zitadel-signing-secret) OPENFGA_API_URL(=http://localhost:8080)
//	DATABASE_URL(=postgres://lazyfga:lazyfga@localhost:5432/lazyfga)
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/antegral-constellation/lazyfga/api/internal/democli"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	os.Exit(run(context.Background(), os.Args, os.Stdout, os.Stderr))
}

// run은 인자를 해석해 데모 run/reset을 실행하고 종료코드를 반환한다(테스트 가능한 본체).
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		_, _ = fmt.Fprintln(stderr, "usage: demo run|reset")
		return 2
	}
	sub := args[1]
	if sub != "run" && sub != "reset" {
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q (use run|reset)\n", sub)
		return 2
	}

	dsn := getenv("DATABASE_URL", "postgres://lazyfga:lazyfga@localhost:5432/lazyfga")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer pool.Close()

	deps := democli.Deps{
		APIBase:       getenv("API_BASE", "http://localhost:8787"),
		AdminToken:    getenv("ADMIN_TOKEN", "devtoken"),
		SigningSecret: getenv("ZITADEL_SIGNING_SECRET", "dev-zitadel-signing-secret"),
		HTTP:          http.DefaultClient,
		StoreID:       democli.NewPgxStoreID(pool),
		Tuples:        democli.NewSDKTupleGateway(getenv("OPENFGA_API_URL", "http://localhost:8080")),
		Out:           stdout,
	}

	if sub == "reset" {
		err = democli.Reset(ctx, deps)
	} else {
		err = democli.Run(ctx, deps)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// getenv는 미설정/빈 값이면 기본값을 쓴다(TS `env ?? default`와 동일 의미).
func getenv(key, def string) string {
	// TS `process.env.X ?? default`와 동일: 미설정일 때만 기본값(명시적 빈 문자열은 보존).
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
