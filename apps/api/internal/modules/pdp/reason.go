// Package pdp는 AuthZEN 평가와 reason 엔진을 담당한다(TS modules/pdp 포팅).
// 단일 질문 템플릿으로 OpenFGA Check 1회를 수행하고, 요청 시 witnessing path(허용) 또는
// missing links(거부)를 재구성한다. 결정과 reason은 같은 모델 버전(pin)을 사용한다.
package pdp

import (
	"context"
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
)

// maxDepth는 reason 그래프 탐색의 최대 상속 깊이다.
const maxDepth = 8

// Gateway는 evaluate/reason이 필요로 하는 OpenFGA 연산이다(테스트에서 fake 주입).
type Gateway interface {
	Check(ctx context.Context, in openfga.CheckInput, opts ...openfga.CheckOption) (bool, error)
	Read(ctx context.Context, in openfga.ReadInput) ([]openfga.ReadTuple, error)
}

// reasonPin은 결정과 reason이 동일 모델 버전/decision을 쓰도록 evaluate가 넘기는 핀이다.
type reasonPin struct {
	decision             bool
	authorizationModelID string
	ir                   *contract.ModelIR
}

// explainCtx는 한 explain 호출의 공유 상태다. visited는 cycle·재수렴 가드((permission|object)).
type explainCtx struct {
	authorizationModelID string
	ir                   *contract.ModelIR
	context              map[string]any
	deps                 Gateway
	visited              map[string]bool
}

func splitObject(object string) (typ, id string, ok bool) {
	i := strings.Index(object, ":")
	if i < 0 {
		return "", "", false
	}
	return object[:i], object[i+1:], true
}

// classifyRoleStep은 (object, role) tuple로 직접/그룹 경유를 판별한다. Check는 true였는데 직접도
// 확인된 그룹도 아니면(와일드카드/비-member userset/페이지 밖 등) incomplete=true로 정직하게 표기한다.
func classifyRoleStep(ctx context.Context, cx *explainCtx, user, role, object, onType string) (contract.ReasonStep, bool, error) {
	tuples, err := cx.deps.Read(ctx, openfga.ReadInput{Object: &object, Relation: &role})
	if err != nil {
		return contract.ReasonStep{}, false, err
	}
	for _, t := range tuples {
		if t.User == user {
			return contract.ReasonStep{Via: "role", Role: role, On: onType, Direct: true}, false, nil
		}
	}
	for _, t := range tuples {
		hash := strings.Index(t.User, "#")
		if hash > 0 && t.User[hash+1:] == "member" {
			groupObject := t.User[:hash] // 예: team:eng
			allowed, err := cx.deps.Check(ctx,
				openfga.CheckInput{User: user, Relation: "member", Object: groupObject, Context: cx.context},
				openfga.WithCheckAuthorizationModelID(cx.authorizationModelID))
			if err != nil {
				return contract.ReasonStep{}, false, err
			}
			if allowed {
				groupType := groupObject
				if gt, _, ok := splitObject(groupObject); ok {
					groupType = gt
				}
				go2 := groupObject
				return contract.ReasonStep{Via: "role", Role: role, On: onType, Direct: false, Group: &groupType, GroupObject: &go2}, false, nil
			}
		}
	}
	return contract.ReasonStep{Via: "role", Role: role, On: onType, Direct: false}, true, nil
}

type witnessResult struct {
	found     bool
	path      []contract.ReasonStep
	truncated bool
}

// findWitness는 role 직접/그룹 → parent 상속 재귀 중 최초 성립 경로를 bounded 탐색한다.
func findWitness(ctx context.Context, cx *explainCtx, user, permission, object string, depth int) (witnessResult, error) {
	if depth > maxDepth {
		return witnessResult{found: false, truncated: true}, nil
	}
	visitKey := permission + "|" + object
	if cx.visited[visitKey] {
		return witnessResult{found: false, truncated: false}, nil // cycle/재수렴 가드.
	}
	cx.visited[visitKey] = true

	typ, _, ok := splitObject(object)
	if !ok {
		return witnessResult{found: false, truncated: false}, nil
	}
	perm := findPermission(cx.ir, typ, permission)
	if perm == nil {
		return witnessResult{found: false, truncated: false}, nil
	}

	for _, role := range perm.GrantedByRoles {
		allowed, err := cx.deps.Check(ctx,
			openfga.CheckInput{User: user, Relation: role, Object: object, Context: cx.context},
			openfga.WithCheckAuthorizationModelID(cx.authorizationModelID))
		if err != nil {
			return witnessResult{}, err
		}
		if allowed {
			step, incomplete, err := classifyRoleStep(ctx, cx, user, role, object, typ)
			if err != nil {
				return witnessResult{}, err
			}
			return witnessResult{found: true, path: []contract.ReasonStep{step}, truncated: incomplete}, nil
		}
	}

	// 형제 parent들을 끝까지 탐색: 한 가지가 깊이 초과여도 다른 깨끗한 witness를 놓치지 않는다.
	sawTruncation := false
	for _, rel := range perm.InheritFromParents {
		rel := rel
		tuples, err := cx.deps.Read(ctx, openfga.ReadInput{Object: &object, Relation: &rel})
		if err != nil {
			return witnessResult{}, err
		}
		for _, t := range tuples {
			child, err := findWitness(ctx, cx, user, permission, t.User, depth+1)
			if err != nil {
				return witnessResult{}, err
			}
			if child.found && child.path != nil {
				parentType := rel
				if pt, _, ok := splitObject(t.User); ok {
					parentType = pt
				}
				parentObject := t.User
				path := append([]contract.ReasonStep{{Via: "parent", Relation: rel, Parent: parentType, ParentObject: &parentObject}}, child.path...)
				return witnessResult{found: true, path: path, truncated: child.truncated}, nil
			}
			if child.truncated {
				sawTruncation = true
			}
		}
	}
	return witnessResult{found: false, truncated: sawTruncation}, nil
}

func findPermission(ir *contract.ModelIR, typ, permission string) *contract.Permission {
	for i := range ir.Resources {
		if ir.Resources[i].Name != typ {
			continue
		}
		for j := range ir.Resources[i].Permissions {
			if ir.Resources[i].Permissions[j].Name == permission {
				return &ir.Resources[i].Permissions[j]
			}
		}
		return nil
	}
	return nil
}

func describePath(user, permission, object string, path []contract.ReasonStep) string {
	parts := make([]string, len(path))
	for i, s := range path {
		if s.Via == "role" {
			suffix := ""
			if s.Direct {
				suffix = " (direct)"
			} else if s.GroupObject != nil {
				suffix = " (via " + *s.GroupObject + " membership)"
			}
			parts[i] = "role " + s.Role + suffix
		} else {
			from := s.Parent
			if s.ParentObject != nil {
				from = *s.ParentObject
			}
			parts[i] = "inherited via " + s.Relation + " from " + from
		}
	}
	return user + " can " + permission + " " + object + ": " + strings.Join(parts, " → ")
}

func describeMissing(object string, links []contract.MissingLink) string {
	bits := make([]string, len(links))
	for i, l := range links {
		if l.Kind == "role" {
			bits[i] = "one of [" + strings.Join(l.AnyOf, ", ") + "] on " + object
		} else {
			bits[i] = l.Needs + " via parent (" + l.Relation + ")"
		}
	}
	joined := "a grant that does not exist in the model"
	if len(bits) > 0 {
		joined = strings.Join(bits, ", or ")
	}
	return "denied: needs " + joined
}

func denyLinks(ir *contract.ModelIR, onType, permission string) []contract.MissingLink {
	links := []contract.MissingLink{}
	perm := findPermission(ir, onType, permission)
	if perm != nil {
		if len(perm.GrantedByRoles) > 0 {
			links = append(links, contract.MissingLink{Kind: "role", AnyOf: perm.GrantedByRoles, On: onType})
		}
		for _, rel := range perm.InheritFromParents {
			links = append(links, contract.MissingLink{Kind: "parent", Relation: rel, Needs: "can_" + permission})
		}
	}
	return links
}

// explain은 결정에 대한 사람이 읽는 reason을 만든다. 허용=witnessing path, 거부=missing links(비대칭).
// pin의 모델 버전/decision을 그대로 사용한다(evaluate와 동일 버전 보장, DB 재조회 없음).
func explain(ctx context.Context, deps Gateway, pin reasonPin, user, permission, object string, reqCtx map[string]any) (contract.ReasonResult, error) {
	cx := &explainCtx{
		authorizationModelID: pin.authorizationModelID,
		ir:                   pin.ir,
		context:              reqCtx,
		deps:                 deps,
		visited:              map[string]bool{},
	}
	onType := object
	if t, _, ok := splitObject(object); ok {
		onType = t
	}

	if pin.decision {
		w, err := findWitness(ctx, cx, user, permission, object, 0)
		if err != nil {
			return contract.ReasonResult{}, err
		}
		if w.found && w.path != nil {
			text := describePath(user, permission, object, w.path)
			if w.truncated {
				tr := true
				return contract.ReasonResult{Decision: true, Path: w.path, Truncated: &tr, Text: text + " (partial)"}, nil
			}
			return contract.ReasonResult{Decision: true, Path: w.path, Text: text}, nil
		}
		tr := true
		return contract.ReasonResult{Decision: true, Truncated: &tr, Text: "allowed via can_" + permission + " (path reconstruction incomplete)"}, nil
	}

	missing := denyLinks(pin.ir, onType, permission)
	return contract.ReasonResult{Decision: false, MissingLinks: missing, Text: describeMissing(object, missing)}, nil
}
