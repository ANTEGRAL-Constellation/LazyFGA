// Command seed-zitadel-rules는 데모용 ZITADEL 매핑 규칙을 idempotent하게 시드한다
// (TS apps/api/scripts/seed-zitadel-rules.ts 포팅). 로직은 internal/democli.SeedZitadelRules에 있다.
//
// 실행: DATABASE_URL=... go run ./cmd/seed-zitadel-rules
// 전제: team#member를 가진 모델이 먼저 발행돼 있어야 grant write가 성공한다(demo run이 담당).
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/antegral-constellation/lazyfga/api/internal/democli"
	"github.com/antegral-constellation/lazyfga/api/internal/modules/idp"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	os.Exit(run(context.Background(), os.Stdout, os.Stderr))
}

// run은 pgx 풀을 조립해 SeedZitadelRules를 실행하고 종료코드를 반환한다(테스트 가능한 본체).
func run(ctx context.Context, stdout, stderr io.Writer) int {
	dsn := getenv("DATABASE_URL", "postgres://lazyfga:lazyfga@localhost:5432/lazyfga")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer pool.Close()

	err = democli.SeedZitadelRules(ctx, democli.SeedDeps{
		Repo:          idp.NewRepo(pool),
		SigningSecret: getenv("ZITADEL_SIGNING_SECRET", "dev-zitadel-signing-secret"),
		Out:           stdout,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// getenv는 미설정/빈 값이면 기본값을 쓴다.
func getenv(key, def string) string {
	// TS `process.env.X ?? default`와 동일: 미설정일 때만 기본값(명시적 빈 문자열은 보존).
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
