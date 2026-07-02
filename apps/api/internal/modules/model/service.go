package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/antegral-constellation/lazyfga/api/internal/compiler"
	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	fga "github.com/openfga/go-sdk"
)

// PublishError는 발행 단계별 실패를 HTTP 상태로 표면화한다(TS PublishError). Detail은 단계별
// 구조체(검증/컴파일/openfga/db)이며 라우트가 {"error": message, "detail": detail}로 낸다.
type PublishError struct {
	Status int
	Detail any
}

func (e *PublishError) Error() string { return fmt.Sprintf("publish failed (%d)", e.Status) }

// 단계별 detail 구조체(TS PublishError.detail 리터럴과 필드/순서 동일).
type validationDetail struct {
	Validation []contract.ValidationError `json:"validation"`
}
type compileDetail struct {
	Compile string `json:"compile"`
	Detail  any    `json:"detail"`
}
type openfgaDetail struct {
	Openfga string `json:"openfga"`
}
type dbDetail struct {
	DB            string `json:"db"`
	OrphanModelID string `json:"orphanModelId"`
}

// Compiler는 IR→(DSL, AuthModel JSON) 컴파일이다(테스트에서 실패 주입).
type Compiler interface {
	Compile(ir *contract.ModelIR) (dsl string, modelJSON []byte, err error)
}

// Gateway는 발행이 필요로 하는 OpenFGA 연산이다.
type Gateway interface {
	WriteAuthorizationModel(ctx context.Context, model fga.WriteAuthorizationModelRequest) (string, error)
}

// Recorder는 감사 기록이다(fire-and-forget).
type Recorder interface {
	Record(action string, data map[string]any, actor string)
}

// defaultCompiler는 내장 compiler.CompileIRToDSL 어댑터다.
type defaultCompiler struct{}

func (defaultCompiler) Compile(ir *contract.ModelIR) (string, []byte, error) {
	return compiler.CompileIRToDSL(ir)
}

// DefaultCompiler는 내장 컴파일러를 반환한다.
func DefaultCompiler() Compiler { return defaultCompiler{} }

// publishModel은 발행 절차(TS model.service.publishModel)를 수행한다:
// 1) ValidateModelIR → 위반 시 422
// 2) Compile → 실패 시 422
// 3) WriteAuthorizationModel → 실패 시 502
// 4) InsertVersion(트랜잭션) → 실패 시 audit db_failure + 500(고아 모델 가능)
// 5) audit → PublishedVersion
func publishModel(ctx context.Context, deps Deps, ir *contract.ModelIR, irRaw json.RawMessage, note *string, createdBy string) (*PublishedVersion, error) {
	if errs := contract.ValidateModelIR(ir); len(errs) > 0 {
		return nil, &PublishError{Status: 422, Detail: validationDetail{Validation: errs}}
	}

	dsl, modelJSON, err := deps.Compiler.Compile(ir)
	if err != nil {
		var ce *compiler.CompileError
		if errors.As(err, &ce) {
			return nil, &PublishError{Status: 422, Detail: compileDetail{Compile: ce.Reason, Detail: ce.Detail}}
		}
		return nil, err // 예상 외 오류 → Hono 기본 500.
	}
	var model fga.WriteAuthorizationModelRequest
	if err := json.Unmarshal(modelJSON, &model); err != nil {
		// 방어적: 공식 transformer 출력이 SDK 타입으로 안 풀리는 경우(정상 경로에선 도달 불가).
		return nil, err
	}

	modelID, err := deps.Gateway.WriteAuthorizationModel(ctx, model)
	if err != nil {
		return nil, &PublishError{Status: 502, Detail: openfgaDetail{Openfga: err.Error()}}
	}

	pv, err := deps.Store.InsertVersion(ctx, InsertParams{
		AuthorizationModelID: modelID,
		IRJSON:               irRaw,
		DSL:                  dsl,
		Note:                 note,
		CreatedBy:            createdBy,
	})
	if err != nil {
		// OpenFGA write는 성공했으나 DB 기록 실패 → 고아 모델 가능(ReadAuthorizationModels로 복구).
		deps.Recorder.Record("model.publish.db_failure", map[string]any{"authorizationModelId": modelID, "error": err.Error()}, createdBy)
		return nil, &PublishError{Status: 500, Detail: dbDetail{DB: err.Error(), OrphanModelID: modelID}}
	}
	deps.Recorder.Record("model.publish", map[string]any{"versionId": pv.ID, "authorizationModelId": modelID}, createdBy)
	return pv, nil
}
