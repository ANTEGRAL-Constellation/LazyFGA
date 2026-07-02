package democli

import (
	"context"
	"errors"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	fga "github.com/openfga/go-sdk"
	fgaclient "github.com/openfga/go-sdk/client"
)

// 프로덕션 어댑터: pgx로 store id를 읽고 go-sdk로 구조 tuple을 write/delete한다.
// cmd/demo/main.go가 조립한다. 데모 오케스트레이션(democli.Run/Reset)은 이들을 인터페이스로만 소비한다.

// NewPgxStoreID는 instance_config.openfga_store_id를 읽는 StoreID 함수를 만든다.
// 행이 없으면(미부트스트랩) ""를 돌려준다(Run은 fatal, Reset은 skip으로 처리).
func NewPgxStoreID(pool *pgxpool.Pool) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		var id string
		err := pool.QueryRow(ctx, `SELECT openfga_store_id FROM instance_config LIMIT 1`).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return id, nil
	}
}

// tupleExecutor는 store-바인딩 클라이언트로 write 요청을 실행한다(테스트 주입 지점).
type tupleExecutor interface {
	exec(ctx context.Context, req fgaclient.ClientWriteRequest) error
}

// SDKTupleGateway는 go-sdk로 단일 구조 tuple을 write/delete한다(TupleGateway 구현).
// store별 클라이언트를 캐시한다(데모는 단일 store만 쓰지만 인터페이스는 storeID를 받는다).
type SDKTupleGateway struct {
	apiURL  string
	newExec func(apiURL, storeID string) (tupleExecutor, error)
	mu      sync.Mutex
	cache   map[string]tupleExecutor
}

// NewSDKTupleGateway는 go-sdk 기반 게이트웨이를 만든다.
func NewSDKTupleGateway(apiURL string) *SDKTupleGateway {
	return &SDKTupleGateway{apiURL: apiURL, newExec: defaultTupleExecutor, cache: map[string]tupleExecutor{}}
}

func (g *SDKTupleGateway) executor(storeID string) (tupleExecutor, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ex, ok := g.cache[storeID]; ok {
		return ex, nil
	}
	ex, err := g.newExec(g.apiURL, storeID)
	if err != nil {
		return nil, err
	}
	g.cache[storeID] = ex
	return ex, nil
}

// Write는 단일 tuple을 transaction 모드로 write한다(멱등/오류 분류는 호출부 democli.Run이 담당).
func (g *SDKTupleGateway) Write(ctx context.Context, storeID string, t Tuple) error {
	ex, err := g.executor(storeID)
	if err != nil {
		return err
	}
	return ex.exec(ctx, fgaclient.ClientWriteRequest{
		Writes: []fga.TupleKey{{User: t.User, Relation: t.Relation, Object: t.Object}},
	})
}

// Delete는 단일 tuple을 삭제한다.
func (g *SDKTupleGateway) Delete(ctx context.Context, storeID string, t Tuple) error {
	ex, err := g.executor(storeID)
	if err != nil {
		return err
	}
	return ex.exec(ctx, fgaclient.ClientWriteRequest{
		Deletes: []fga.TupleKeyWithoutCondition{{User: t.User, Relation: t.Relation, Object: t.Object}},
	})
}

// defaultTupleExecutor는 go-sdk store-바인딩 클라이언트 executor를 만든다(프로덕션 글루).
func defaultTupleExecutor(apiURL, storeID string) (tupleExecutor, error) {
	client, err := fgaclient.NewSdkClient(&fgaclient.ClientConfiguration{ApiUrl: apiURL, StoreId: storeID})
	if err != nil {
		return nil, err
	}
	return &sdkExecutor{client: client}, nil
}

type sdkExecutor struct{ client *fgaclient.OpenFgaClient }

func (e *sdkExecutor) exec(ctx context.Context, req fgaclient.ClientWriteRequest) error {
	_, err := e.client.Write(ctx).Body(req).Execute()
	return err
}
