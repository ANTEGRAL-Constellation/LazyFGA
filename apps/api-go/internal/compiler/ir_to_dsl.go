package compiler

import (
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/openfga/language/pkg/go/transformer"
)

// CompileError는 컴파일 실패 원인을 담는다. Reason: "IR_INVALID"(방어적 재검증 실패) |
// "JSON_TRANSFORM_FAILED"(공식 transformer가 emit된 DSL을 거부).
type CompileError struct {
	Reason string
	Detail any
}

func (e *CompileError) Error() string { return "compileIrToDsl failed: " + e.Reason }

// serializeSubject: SubjectRef → DSL 토큰. user | <group>#member (+ " with <cond>" if conditioned).
func serializeSubject(ref contract.SubjectRef) string {
	base := "user"
	if ref.Kind != "user" {
		base = ref.Group + "#" + ref.Relation
	}
	if ref.Condition != nil {
		return base + " with " + *ref.Condition
	}
	return base
}

// serializeSubjects: type restriction 직렬화 [a, b, ...](IR 배열 순서 유지).
func serializeSubjects(refs []contract.SubjectRef) string {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = serializeSubject(r)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// emitDsl: ModelIR → OpenFGA DSL 문자열(결정적). emit 순서: header → groups(입력순) →
// resources(입력순; relation은 parents→roles→permissions) → 최상위 condition 블록(입력순).
func emitDsl(ir *contract.ModelIR) string {
	lines := []string{"model", "  schema 1.1", "type user"}

	for _, g := range ir.Groups {
		lines = append(lines,
			"type "+g.Name,
			"  relations",
			"    define member: "+serializeSubjects(g.MemberTypes),
		)
	}

	for _, r := range ir.Resources {
		lines = append(lines, "type "+r.Name)
		var relLines []string

		for _, p := range r.Parents {
			relLines = append(relLines, "    define "+p.RelationName+": ["+strings.Join(p.ParentTypes, ", ")+"]")
		}
		for _, role := range r.Roles {
			relLines = append(relLines, "    define "+role.Name+": "+serializeSubjects(role.AssignableBy))
		}
		for _, perm := range r.Permissions {
			union := make([]string, 0, len(perm.GrantedByRoles)+len(perm.InheritFromParents))
			union = append(union, perm.GrantedByRoles...)
			for _, rel := range perm.InheritFromParents {
				union = append(union, "can_"+perm.Name+" from "+rel)
			}
			relLines = append(relLines, "    define can_"+perm.Name+": "+strings.Join(union, " or "))
		}

		if len(relLines) > 0 {
			lines = append(lines, "  relations")
			lines = append(lines, relLines...)
		}
	}

	// 최상위 condition 블록(타입 뒤). 입력순 유지(결정적).
	for i := range ir.Conditions {
		decl, cel := ConditionToCel(&ir.Conditions[i])
		lines = append(lines, decl+" {", "  "+cel, "}")
	}

	return strings.Join(lines, "\n")
}

// CompileIRToDSL은 ModelIR을 결정적 .fga DSL 문자열과 AuthorizationModel JSON(공식
// openfga/language transformer 경유)으로 컴파일한다. 호출 전 ValidateModelIR 통과 전제이며
// 방어적으로 재검증한다. 실패 시 *CompileError를 반환한다.
func CompileIRToDSL(ir *contract.ModelIR) (dsl string, modelJSON []byte, err error) {
	if errs := contract.ValidateModelIR(ir); len(errs) > 0 {
		return "", nil, &CompileError{Reason: "IR_INVALID", Detail: errs}
	}

	dsl = emitDsl(ir)

	jsonStr, terr := transformer.TransformDSLToJSON(dsl)
	if terr != nil {
		return "", nil, &CompileError{Reason: "JSON_TRANSFORM_FAILED", Detail: terr}
	}

	return dsl, []byte(jsonStr), nil
}
