package policy

import (
	"context"
	"errors"
	"regexp"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/modules/model"
	"github.com/jackc/pgx/v5/pgconn"
)

// PolicyError는 정책 작업 실패를 HTTP 상태로 표면화한다(TS PolicyError). Detail은 라우트가
// {"error": detail}로 내는 사람이 읽는 문자열이다.
type PolicyError struct {
	Status int // 409 | 422
	Detail string
}

func (e *PolicyError) Error() string { return e.Detail }

// slugRE는 정책 id slug 규칙이다.
var slugRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// ModelReader는 대상 검증을 위해 현재 발행 모델을 읽는다(consumer-owned 인터페이스).
type ModelReader interface {
	CurrentVersion(ctx context.Context) (*model.Version, error)
}

// createInput/patchInput는 서비스 입력이다.
type createInput struct {
	ID           string
	Permission   string
	ResourceType string
	Description  *string
}
type patchInput struct {
	Permission   *string
	ResourceType *string
	Description  *string
}

// assertModelHasTarget은 현재 발행 모델에 resourceType 타입과 permission이 실제 존재하는지 검증한다.
// PolicyError(422) 또는 raw error(모델 조회 실패 → Hono 500)를 반환한다.
func assertModelHasTarget(ctx context.Context, deps Deps, permission, resourceType string) error {
	current, err := deps.Model.CurrentVersion(ctx)
	if err != nil {
		return err
	}
	if current == nil {
		return &PolicyError{Status: 422, Detail: "no model published yet; publish a model first"}
	}
	ir, err := current.IR()
	if err != nil {
		return err
	}
	var resource *contract.ResourceType
	for i := range ir.Resources {
		if ir.Resources[i].Name == resourceType {
			resource = &ir.Resources[i]
			break
		}
	}
	if resource == nil {
		return &PolicyError{Status: 422, Detail: `current model has no resource type "` + resourceType + `"`}
	}
	for _, p := range resource.Permissions {
		if p.Name == permission {
			return nil
		}
	}
	return &PolicyError{Status: 422, Detail: `"` + resourceType + `" has no permission "can_` + permission + `" in the current model`}
}

// createPolicy는 정책을 생성한다(slug → dup id → dup (perm,resource) → 대상 검증 → insert + 23505 backstop).
func createPolicy(ctx context.Context, deps Deps, input createInput) (*contract.Policy, error) {
	if !slugRE.MatchString(input.ID) {
		return nil, &PolicyError{Status: 422, Detail: "id must be a slug matching " + slugRE.String()}
	}
	existing, err := deps.Store.FindByID(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, &PolicyError{Status: 409, Detail: `policy id "` + input.ID + `" already exists`}
	}
	clash, err := deps.Store.FindByActionResource(ctx, input.Permission, input.ResourceType)
	if err != nil {
		return nil, err
	}
	if clash != nil {
		return nil, &PolicyError{Status: 409, Detail: "a policy for (" + input.Permission + ", " + input.ResourceType + ") already exists"}
	}
	if terr := assertModelHasTarget(ctx, deps, input.Permission, input.ResourceType); terr != nil {
		return nil, terr
	}
	p, err := deps.Store.InsertPolicy(ctx, contract.Policy{
		ID:           input.ID,
		Permission:   input.Permission,
		ResourceType: input.ResourceType,
		Description:  input.Description,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, &PolicyError{Status: 409, Detail: "policy already exists (id or (permission, resourceType))"}
		}
		return nil, err
	}
	return p, nil
}

// editPolicy는 정책을 patch merge로 수정한다(유일성 clash → 409, 대상 검증 → 422, 23505 backstop).
func editPolicy(ctx context.Context, deps Deps, id string, patch patchInput) (*contract.Policy, error) {
	existing, err := deps.Store.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, &PolicyError{Status: 422, Detail: `policy "` + id + `" not found`} // 라우트가 사전 404, 여기는 경합 대비.
	}
	permission := existing.Permission
	if patch.Permission != nil {
		permission = *patch.Permission
	}
	resourceType := existing.ResourceType
	if patch.ResourceType != nil {
		resourceType = *patch.ResourceType
	}

	clash, err := deps.Store.FindByActionResource(ctx, permission, resourceType)
	if err != nil {
		return nil, err
	}
	if clash != nil && clash.ID != id {
		return nil, &PolicyError{Status: 409, Detail: "a policy for (" + permission + ", " + resourceType + ") already exists"}
	}
	if terr := assertModelHasTarget(ctx, deps, permission, resourceType); terr != nil {
		return nil, terr
	}

	desc := existing.Description
	if patch.Description != nil {
		desc = patch.Description
	}
	updated, err := deps.Store.UpdatePolicy(ctx, id, UpdateParams{Permission: permission, ResourceType: resourceType, Description: desc})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, &PolicyError{Status: 409, Detail: "a policy for (" + permission + ", " + resourceType + ") already exists"}
		}
		return nil, err
	}
	if updated == nil {
		return nil, &PolicyError{Status: 422, Detail: `policy "` + id + `" not found`}
	}
	return updated, nil
}

// isUniqueViolation은 Postgres UNIQUE 위반(23505)인지 판별한다.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
