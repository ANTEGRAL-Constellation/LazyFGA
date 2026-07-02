// Package permission은 구조적 권한 grant/revoke/list를 담당한다(TS modules/permission 포팅, LFGA-20).
// 발행본 모델로 검증 → gateway로 단일 tuple write/delete → audit. 멱등 write는 흡수(200 no-op),
// transient는 502, 그 외 결정적 4xx는 400 backstop.
package permission

import (
	"context"
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/modules/model"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga/writeerror"
	fga "github.com/openfga/go-sdk"
)

// GrantError는 grant/revoke/list 실패를 HTTP 상태로 표면화한다(TS GrantError).
type GrantError struct {
	Status int // 400 | 404 | 502
	Code   string
	Detail string
}

func (e *GrantError) Error() string { return e.Detail }

// Gateway는 permission이 필요로 하는 OpenFGA 연산이다(테스트에서 fake 주입).
type Gateway interface {
	Write(ctx context.Context, in openfga.WriteInput, opts ...openfga.WriteOption) error
	Read(ctx context.Context, in openfga.ReadInput) ([]openfga.ReadTuple, error)
}

// ModelReader는 현재 발행 모델을 읽는다(consumer-owned).
type ModelReader interface {
	CurrentVersion(ctx context.Context) (*model.Version, error)
}

// Recorder는 감사 기록이다(fire-and-forget).
type Recorder interface {
	Record(action string, data map[string]any, actor string)
}

// publishedModel은 발행본 ModelIR + OpenFGA model id를 해석한다. 미발행 → GrantError(404).
func publishedModel(ctx context.Context, deps Deps) (*contract.ModelIR, string, error) {
	cur, err := deps.Model.CurrentVersion(ctx)
	if err != nil {
		return nil, "", err
	}
	if cur == nil {
		return nil, "", &GrantError{Status: 404, Code: "no_published_model", Detail: "no model has been published yet"}
	}
	ir, err := cur.IR()
	if err != nil {
		return nil, "", err
	}
	return ir, cur.AuthorizationModelID, nil
}

// interpretWriteError는 OpenFGA write/delete 오류를 결과로 해석한다(순수, 테스트 가능).
// 멱등 → noop(상태 변화 아님), transient → 502, 그 외 결정적 4xx → 400 backstop.
func interpretWriteError(e error, op writeerror.Op) (noop bool, gerr *GrantError) {
	c := writeerror.ClassifyWriteError(e, op)
	if c.Idempotent {
		return true, nil
	}
	if c.Transient {
		return false, &GrantError{Status: 502, Code: "openfga_unavailable", Detail: e.Error()}
	}
	return false, &GrantError{Status: 400, Code: "openfga_invalid_input", Detail: e.Error()}
}

// grant는 권한을 부여한다. 새 tuple → (true, nil)(감사됨), 이미 존재 → (false, nil)(no-op, 미감사).
func grant(ctx context.Context, deps Deps, req *contract.GrantRequest, actor string) (bool, error) {
	ir, modelID, err := publishedModel(ctx, deps)
	if err != nil {
		return false, err
	}
	if v := contract.ValidateGrant(ir, req); !v.OK {
		return false, &GrantError{Status: 400, Code: v.Code, Detail: v.Message}
	}
	writes := []fga.TupleKey{toFgaTupleKey(contract.GrantTupleKeyOf(req))}
	if err := deps.Gateway.Write(ctx, openfga.WriteInput{Writes: writes}, openfga.WithWriteAuthorizationModelID(modelID)); err != nil {
		noop, gerr := interpretWriteError(err, writeerror.OpWrite)
		if gerr != nil {
			return false, gerr
		}
		_ = noop
		return false, nil
	}
	// TS JSON.stringify는 undefined 값을 키째 생략한다 — condition 부재 시 키를 넣지 않는다.
	data := map[string]any{
		"subject":  req.Subject,
		"relation": req.Relation,
		"resource": req.Resource,
	}
	if req.Condition != nil {
		data["condition"] = req.Condition
	}
	deps.Recorder.Record("permission.grant", data, actor)
	return true, nil
}

// revoke는 권한을 회수한다. 실제 삭제 → (true, nil)(감사됨), 이미 없음 → (false, nil)(no-op, 미감사).
func revoke(ctx context.Context, deps Deps, req *contract.RevokeRequest, actor string) (bool, error) {
	ir, modelID, err := publishedModel(ctx, deps)
	if err != nil {
		return false, err
	}
	if v := contract.ValidateRevoke(ir, req); !v.OK {
		return false, &GrantError{Status: 400, Code: v.Code, Detail: v.Message}
	}
	deletes := []fga.TupleKeyWithoutCondition{toFgaTupleKeyWithoutCondition(contract.RevokeTupleKeyOf(req))}
	if err := deps.Gateway.Write(ctx, openfga.WriteInput{Deletes: deletes}, openfga.WithWriteAuthorizationModelID(modelID)); err != nil {
		noop, gerr := interpretWriteError(err, writeerror.OpDelete)
		if gerr != nil {
			return false, gerr
		}
		_ = noop
		return false, nil
	}
	deps.Recorder.Record("permission.revoke", map[string]any{
		"subject":  req.Subject,
		"relation": req.Relation,
		"resource": req.Resource,
	}, actor)
	return true, nil
}

// listByResource는 리소스 위 배정 목록(단일 Read)을 배정 가능 relation으로만 필터해 반환한다.
func listByResource(ctx context.Context, deps Deps, resource contract.ResourceRef) ([]contract.GrantEntry, error) {
	ir, _, err := publishedModel(ctx, deps)
	if err != nil {
		return nil, err
	}
	object := resource.Type + ":" + resource.ID
	tuples, err := readTuples(ctx, deps, openfga.ReadInput{Object: &object})
	if err != nil {
		return nil, err
	}
	entries := make([]contract.GrantEntry, 0)
	for _, t := range tuples {
		if contract.IsAssignableRelation(ir, resource.Type, t.Relation) {
			entries = append(entries, tupleToEntry(t))
		}
	}
	return entries, nil
}

// listBySubject는 주체가 보유한 배정 목록을 반환한다. resourceType 지정 → Read 1회,
// 미지정 → 발행본의 모든 resource/group 타입에 fan-out 후 병합.
func listBySubject(ctx context.Context, deps Deps, subject contract.GrantSubject, resourceType *string) ([]contract.GrantEntry, error) {
	ir, _, err := publishedModel(ctx, deps)
	if err != nil {
		return nil, err
	}
	user := contract.SubjectToUser(subject)
	var types []string
	if resourceType != nil {
		types = []string{*resourceType}
	} else {
		for _, r := range ir.Resources {
			types = append(types, r.Name)
		}
		for _, g := range ir.Groups {
			types = append(types, g.Name)
		}
	}
	entries := make([]contract.GrantEntry, 0)
	for _, tp := range types {
		object := tp + ":"
		tuples, err := readTuples(ctx, deps, openfga.ReadInput{User: &user, Object: &object})
		if err != nil {
			return nil, err
		}
		for _, t := range tuples {
			if contract.IsAssignableRelation(ir, tp, t.Relation) {
				entries = append(entries, tupleToEntry(t))
			}
		}
	}
	return entries, nil
}

// readTuples는 Read 실패를 분류한다: transient(5xx/429/네트워크) → 502, 결정적 4xx → 400.
func readTuples(ctx context.Context, deps Deps, in openfga.ReadInput) ([]openfga.ReadTuple, error) {
	tuples, err := deps.Gateway.Read(ctx, in)
	if err != nil {
		if writeerror.IsTransientAPIError(err) {
			return nil, &GrantError{Status: 502, Code: "openfga_unavailable", Detail: err.Error()}
		}
		return nil, &GrantError{Status: 400, Code: "openfga_invalid_input", Detail: err.Error()}
	}
	return tuples, nil
}

// tupleToEntry는 read tuple을 GrantEntry로 변환한다(userset은 #relation 분리).
func tupleToEntry(t openfga.ReadTuple) contract.GrantEntry {
	var subject contract.GrantSubject
	if hash := strings.Index(t.User, "#"); hash >= 0 {
		obj := splitObjectRef(t.User[:hash])
		rel := t.User[hash+1:]
		subject = contract.GrantSubject{Type: obj.Type, ID: obj.ID, Relation: &rel}
	} else {
		obj := splitObjectRef(t.User)
		subject = contract.GrantSubject{Type: obj.Type, ID: obj.ID}
	}
	entry := contract.GrantEntry{Subject: subject, Relation: t.Relation, Resource: splitObjectRef(t.Object)}
	if t.Condition != nil {
		entry.Condition = &contract.GrantCondition{Name: t.Condition.Name, Context: t.Condition.Context}
	}
	return entry
}

// splitObjectRef는 type:id 를 관대하게 분리한다(id 없으면 빈 문자열).
func splitObjectRef(s string) contract.ResourceRef {
	if i := strings.Index(s, ":"); i >= 0 {
		return contract.ResourceRef{Type: s[:i], ID: s[i+1:]}
	}
	return contract.ResourceRef{Type: s, ID: ""}
}

// toFgaTupleKey/toFgaTupleKeyWithoutCondition는 계약 tuple 키를 SDK 타입으로 변환한다.
func toFgaTupleKey(k contract.GrantTupleKey) fga.TupleKey {
	tk := fga.TupleKey{User: k.User, Relation: k.Relation, Object: k.Object}
	if k.Condition != nil {
		cond := fga.RelationshipCondition{Name: k.Condition.Name}
		if k.Condition.Context != nil {
			ctxMap := map[string]interface{}(k.Condition.Context)
			cond.Context = &ctxMap
		}
		tk.Condition = &cond
	}
	return tk
}

func toFgaTupleKeyWithoutCondition(k contract.TupleRef) fga.TupleKeyWithoutCondition {
	return fga.TupleKeyWithoutCondition{User: k.User, Relation: k.Relation, Object: k.Object}
}
