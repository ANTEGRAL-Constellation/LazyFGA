// Package openfga는 단일 store에 바인딩된 OpenFGA 진입점(Gateway)을 제공한다.
// TS openfga/gateway.ts를 포팅한다: 부팅 시 store를 보장(없으면 생성)하고 이후 모든
// 호출이 이 store를 사용한다. store id 로드/영속은 콜백으로 위임해 이 패키지를 DB-비의존으로 둔다.
package openfga

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	fga "github.com/openfga/go-sdk"
	fgaclient "github.com/openfga/go-sdk/client"
)

// CheckInput은 관계 질의 입력이다.
type CheckInput struct {
	User     string
	Relation string
	Object   string
	// Context는 조건 평가용 부가 컨텍스트(있을 때만).
	Context map[string]any
}

// ReadInput은 tuple 조회 필터다. 각 필드는 선택(nil이면 해당 축으로 필터링하지 않음).
type ReadInput struct {
	User     *string
	Relation *string
	Object   *string
}

// TupleCondition은 tuple에 부착된 조건이다(lazyfga-14/20).
type TupleCondition struct {
	Name    string
	Context map[string]any
}

// ReadTuple은 조회된 tuple 1건이다.
type ReadTuple struct {
	User      string
	Relation  string
	Object    string
	Condition *TupleCondition
}

// WriteInput은 transaction-mode write/delete 입력이다.
type WriteInput struct {
	Writes  []fga.TupleKey
	Deletes []fga.TupleKeyWithoutCondition
}

// BootstrapOptions는 store id 해석에 쓰이는 콜백 묶음이다.
type BootstrapOptions struct {
	// EnvStoreID는 env LAZYFGA_STORE_ID(선택, 빈 문자열이면 미지정).
	EnvStoreID string
	// LoadStoredStoreID는 lazyFGA DB(instance_config)에 저장된 store id를 로드한다.
	LoadStoredStoreID func(ctx context.Context) (string, error)
	// PersistStoreID는 확정된 store id를 영속한다.
	PersistStoreID func(ctx context.Context, storeID string) error
}

// checkOptions/writeOptions는 authorization model 핀 등 선택 인자를 담는다.
type checkOptions struct{ authorizationModelID string }
type writeOptions struct{ authorizationModelID string }

// CheckOption은 Check 호출의 선택 인자다.
type CheckOption func(*checkOptions)

// WriteOption은 Write 호출의 선택 인자다.
type WriteOption func(*writeOptions)

// WithCheckAuthorizationModelID는 발행본 모델 기준으로 check를 고정한다.
func WithCheckAuthorizationModelID(id string) CheckOption {
	return func(o *checkOptions) { o.authorizationModelID = id }
}

// WithWriteAuthorizationModelID는 발행본 모델 기준으로 write를 검증하게 한다.
func WithWriteAuthorizationModelID(id string) WriteOption {
	return func(o *writeOptions) { o.authorizationModelID = id }
}

// Gateway는 부트스트랩 후 단일 store에 바인딩되는 OpenFGA 진입점이다.
type Gateway interface {
	Bootstrap(ctx context.Context, opts BootstrapOptions) (storeID string, err error)
	StoreID() (string, error)
	Ping(ctx context.Context) bool
	Check(ctx context.Context, in CheckInput, opts ...CheckOption) (allowed bool, err error)
	Read(ctx context.Context, in ReadInput) ([]ReadTuple, error)
	Write(ctx context.Context, in WriteInput, opts ...WriteOption) error
	WriteAuthorizationModel(ctx context.Context, model fga.WriteAuthorizationModelRequest) (modelID string, err error)
}

// sdkClient는 SDK 클라이언트에서 gateway가 쓰는 메서드만 추린 인터페이스로,
// 테스트에서 대체 가능하게 한다. *fgaclient.OpenFgaClient가 이를 만족한다.
type gatewayImpl struct {
	apiURL string
	logger *slog.Logger
	// mgmt는 store 관리(create/get/list)용. 특정 store에 바인딩되지 않는다.
	mgmt *fgaclient.OpenFgaClient
	// mu는 storeID/client 바인딩을 보호한다. 서버가 리슨 중에 백그라운드 부트스트랩이
	// 쓰기(Bootstrap)를 하고 핸들러가 읽으므로(Check/Read/Write) 동기화가 필수다.
	mu      sync.RWMutex
	storeID string
	// client는 부트스트랩 후 store에 바인딩된 클라이언트.
	client *fgaclient.OpenFgaClient
	// newStoreClient는 store-바인딩 클라이언트 생성자(테스트 주입 지점).
	newStoreClient func(apiURL, storeID string) (*fgaclient.OpenFgaClient, error)
}

// NewGateway는 관리 클라이언트를 구성한 gateway를 만든다. apiURL이 잘못되면 오류.
func NewGateway(apiURL string, logger *slog.Logger) (Gateway, error) {
	if logger == nil {
		logger = slog.Default()
	}
	mgmt, err := fgaclient.NewSdkClient(&fgaclient.ClientConfiguration{ApiUrl: apiURL})
	if err != nil {
		return nil, fmt.Errorf("openfga: create management client: %w", err)
	}
	return &gatewayImpl{
		apiURL:         apiURL,
		logger:         logger,
		mgmt:           mgmt,
		newStoreClient: defaultStoreClient,
	}, nil
}

func defaultStoreClient(apiURL, storeID string) (*fgaclient.OpenFgaClient, error) {
	return fgaclient.NewSdkClient(&fgaclient.ClientConfiguration{ApiUrl: apiURL, StoreId: storeID})
}

func (g *gatewayImpl) Bootstrap(ctx context.Context, opts BootstrapOptions) (string, error) {
	// candidate = envStoreId ?? loadStoredStoreId()
	candidate := opts.EnvStoreID
	if candidate == "" {
		loaded, err := opts.LoadStoredStoreID(ctx)
		if err != nil {
			return "", err
		}
		candidate = loaded
	}

	storeID := ""
	if candidate != "" && g.storeExists(ctx, candidate) {
		storeID = candidate
	} else if candidate != "" && opts.EnvStoreID != "" {
		// env로 명시한 store가 OpenFGA에 없음 → 조용히 새로 만들면 데이터 skew 위험.
		g.logger.Warn("LAZYFGA_STORE_ID not found in OpenFGA; creating a new store",
			"storeId", candidate,
			"hint", "if OpenFGA was reset, lazyFGA Postgres data may now reference a fresh empty store")
	}

	if storeID == "" {
		created, err := g.mgmt.CreateStore(ctx).Body(fgaclient.ClientCreateStoreRequest{Name: "lazyfga"}).Execute()
		if err != nil {
			return "", err
		}
		storeID = created.Id
	}

	if err := opts.PersistStoreID(ctx, storeID); err != nil {
		return "", err
	}
	client, err := g.newStoreClient(g.apiURL, storeID)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	g.storeID = storeID
	g.client = client
	g.mu.Unlock()
	return storeID, nil
}

func (g *gatewayImpl) StoreID() (string, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.storeID == "" {
		return "", fmt.Errorf("openfga: gateway not bootstrapped")
	}
	return g.storeID, nil
}

func (g *gatewayImpl) Ping(ctx context.Context) bool {
	if _, err := g.mgmt.ListStores(ctx).Execute(); err != nil {
		return false
	}
	return true
}

func (g *gatewayImpl) Check(ctx context.Context, in CheckInput, opts ...CheckOption) (bool, error) {
	client, err := g.requireClient()
	if err != nil {
		return false, err
	}
	var o checkOptions
	for _, opt := range opts {
		opt(&o)
	}
	body := fgaclient.ClientCheckRequest{
		User:     in.User,
		Relation: in.Relation,
		Object:   in.Object,
	}
	if in.Context != nil {
		ctxCopy := map[string]any(in.Context)
		body.Context = &ctxCopy
	}
	req := client.Check(ctx).Body(body)
	if o.authorizationModelID != "" {
		req = req.Options(fgaclient.ClientCheckOptions{AuthorizationModelId: &o.authorizationModelID})
	}
	res, err := req.Execute()
	if err != nil {
		return false, err
	}
	return res.GetAllowed(), nil
}

func (g *gatewayImpl) Read(ctx context.Context, in ReadInput) ([]ReadTuple, error) {
	client, err := g.requireClient()
	if err != nil {
		return nil, err
	}
	// OpenFGA Read는 페이지네이션된다(기본 ~50). continuation_token이 빌 때까지 전 페이지를 모은다
	// — 안 그러면 권한 목록이 첫 페이지로 조용히 잘려 감사/회수가 불완전해진다(LFGA-20 review).
	tuples := make([]ReadTuple, 0)
	var continuationToken string
	for {
		opts := fgaclient.ClientReadOptions{}
		if continuationToken != "" {
			token := continuationToken
			opts.ContinuationToken = &token
		}
		res, err := client.Read(ctx).Body(fgaclient.ClientReadRequest{
			User:     in.User,
			Relation: in.Relation,
			Object:   in.Object,
		}).Options(opts).Execute()
		if err != nil {
			return nil, err
		}
		for _, t := range res.Tuples {
			tuple := ReadTuple{
				User:     t.Key.User,
				Relation: t.Key.Relation,
				Object:   t.Key.Object,
			}
			if t.Key.Condition != nil {
				cond := &TupleCondition{Name: t.Key.Condition.Name}
				if t.Key.Condition.Context != nil {
					cond.Context = map[string]any(*t.Key.Condition.Context)
				}
				tuple.Condition = cond
			}
			tuples = append(tuples, tuple)
		}
		continuationToken = res.ContinuationToken
		if continuationToken == "" {
			break
		}
	}
	return tuples, nil
}

func (g *gatewayImpl) Write(ctx context.Context, in WriteInput, opts ...WriteOption) error {
	client, err := g.requireClient()
	if err != nil {
		return err
	}
	var o writeOptions
	for _, opt := range opts {
		opt(&o)
	}
	// 기본 transaction 모드(SDK 기본). duplicate write / missing delete는 OpenFGA가 400
	// invalid-input으로 던지며, 호출부가 writeerror.ClassifyWriteError로 멱등 흡수한다(lazyfga-20).
	body := fgaclient.ClientWriteRequest{Writes: in.Writes, Deletes: in.Deletes}
	req := client.Write(ctx).Body(body)
	if o.authorizationModelID != "" {
		req = req.Options(fgaclient.ClientWriteOptions{AuthorizationModelId: &o.authorizationModelID})
	}
	_, err = req.Execute()
	return err
}

func (g *gatewayImpl) WriteAuthorizationModel(ctx context.Context, model fga.WriteAuthorizationModelRequest) (string, error) {
	client, err := g.requireClient()
	if err != nil {
		return "", err
	}
	res, err := client.WriteAuthorizationModel(ctx).Body(model).Execute()
	if err != nil {
		return "", err
	}
	return res.AuthorizationModelId, nil
}

func (g *gatewayImpl) requireClient() (*fgaclient.OpenFgaClient, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.client == nil {
		return nil, fmt.Errorf("openfga: gateway not bootstrapped")
	}
	return g.client, nil
}

func (g *gatewayImpl) storeExists(ctx context.Context, id string) bool {
	_, err := g.mgmt.GetStore(ctx).Options(fgaclient.ClientGetStoreOptions{StoreId: &id}).Execute()
	return err == nil
}

// ResolveCheckAuthorizationModelID는 옵션 적용 결과의 모델 핀을 돌려준다(테스트 fake의
// 핀 검증용 — 모듈 테스트가 비공개 옵션 구조체를 볼 수 없으므로 여기서 해석해 준다).
func ResolveCheckAuthorizationModelID(opts ...CheckOption) string {
	var o checkOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o.authorizationModelID
}

// ResolveWriteAuthorizationModelID는 Write 옵션의 모델 핀을 돌려준다(테스트 fake용).
func ResolveWriteAuthorizationModelID(opts ...WriteOption) string {
	var o writeOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o.authorizationModelID
}
