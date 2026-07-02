package contract

import (
	"regexp"
	"strings"
)

// 권한 grant/revoke 계약 + 순수 검증기(LFGA-20). OpenFGA 없이 단위 테스트 가능한 조기 UX 게이트.
// 참고: Go는 함수·타입 이름 공유가 불가하므로 빌더는 GrantTupleKeyOf/RevokeTupleKeyOf로 둔다
// (타입 GrantTupleKey/TupleRef와 충돌 회피). 나머지 의미는 TS와 동일하다.

// type:id 의 id에 :,#,*,공백(ECMAScript \s 집합) 금지 — tuple 구조/OpenFGA 의미 파손 방지.
var forbiddenInID = regexp.MustCompile(`[:#*\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`)

// ResourceRef: type:id 파싱 결과.
type ResourceRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// GrantSubject: 배정 대상 주체. relation 없으면 concrete user, 있으면 group member userset.
// relation은 optional 판별을 위해 *string(빈 문자열과 부재를 구분, TS truthiness 재현).
type GrantSubject struct {
	Type     string  `json:"type"`
	ID       string  `json:"id"`
	Relation *string `json:"relation,omitempty"`
}

// GrantCondition: {name, context?}. context는 부재 시 생략, non-nil(빈 객체 포함) 시 포함
// (TS `context ? {name, context} : {name}` truthiness 재현).
type GrantCondition struct {
	Name    string         `json:"name"`
	Context map[string]any `json:"context,omitempty"`
}

func (c GrantCondition) MarshalJSON() ([]byte, error) {
	if c.Context == nil {
		return marshalNoEscape(struct {
			Name string `json:"name"`
		}{c.Name})
	}
	return marshalNoEscape(struct {
		Name    string         `json:"name"`
		Context map[string]any `json:"context"`
	}{c.Name, c.Context})
}

// GrantRequest: resource.type 위에서 직접 배정 가능한 relation에 tuple write.
type GrantRequest struct {
	Subject   GrantSubject    `json:"subject"`
	Relation  string          `json:"relation"`
	Resource  ResourceRef     `json:"resource"`
	Condition *GrantCondition `json:"condition,omitempty"`
}

// RevokeRequest: condition 없는 GrantRequest.
type RevokeRequest struct {
	Subject  GrantSubject `json:"subject"`
	Relation string       `json:"relation"`
	Resource ResourceRef  `json:"resource"`
}

// GrantTupleKey: grantTupleKey 결과(구조적으로 OpenFGA TupleKey 호환).
type GrantTupleKey struct {
	User      string          `json:"user"`
	Relation  string          `json:"relation"`
	Object    string          `json:"object"`
	Condition *GrantCondition `json:"condition,omitempty"`
}

// TupleRef: revokeTupleKey 결과(조건 없음).
type TupleRef struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// GrantEntry: 현재 배정 1건(GET /grants 응답 원소).
type GrantEntry struct {
	Subject   GrantSubject    `json:"subject"`
	Relation  string          `json:"relation"`
	Resource  ResourceRef     `json:"resource"`
	Condition *GrantCondition `json:"condition,omitempty"`
}

// GrantValidation: {ok:true} | {ok:false, code, message}.
type GrantValidation struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func okValidation() GrantValidation { return GrantValidation{OK: true} }
func failValidation(code, message string) GrantValidation {
	return GrantValidation{OK: false, Code: code, Message: message}
}

// SubjectToUser: subject → OpenFGA user 문자열. user:alice 또는 team:eng#member.
func SubjectToUser(s GrantSubject) string {
	if s.Relation != nil && *s.Relation != "" {
		return s.Type + ":" + s.ID + "#" + *s.Relation
	}
	return s.Type + ":" + s.ID
}

// GrantTupleKeyOf: grantTupleKey 포트.
func GrantTupleKeyOf(req *GrantRequest) GrantTupleKey {
	key := GrantTupleKey{
		User:     SubjectToUser(req.Subject),
		Relation: req.Relation,
		Object:   req.Resource.Type + ":" + req.Resource.ID,
	}
	if req.Condition != nil {
		key.Condition = &GrantCondition{Name: req.Condition.Name, Context: req.Condition.Context}
	}
	return key
}

// RevokeTupleKeyOf: revokeTupleKey 포트.
func RevokeTupleKeyOf(req *RevokeRequest) TupleRef {
	return TupleRef{
		User:     SubjectToUser(req.Subject),
		Relation: req.Relation,
		Object:   req.Resource.Type + ":" + req.Resource.ID,
	}
}

// assignableSubjects: (type, relation)이 직접 배정 가능하면 허용 SubjectRef + true, 아니면 false.
func assignableSubjects(model *ModelIR, typ, relation string) ([]SubjectRef, bool) {
	for i := range model.Resources {
		if model.Resources[i].Name == typ {
			for j := range model.Resources[i].Roles {
				if model.Resources[i].Roles[j].Name == relation {
					return model.Resources[i].Roles[j].AssignableBy, true
				}
			}
			return nil, false
		}
	}
	for i := range model.Groups {
		if model.Groups[i].Name == typ {
			if relation == "member" {
				return model.Groups[i].MemberTypes, true
			}
			return nil, false
		}
	}
	return nil, false
}

// subjectMatchesRef: subject가 이 SubjectRef와 type/userset 형태가 일치하는가(조건은 별도).
func subjectMatchesRef(ref SubjectRef, s GrantSubject) bool {
	if ref.Kind == "user" {
		return s.Relation == nil && s.Type == "user"
	}
	return s.Relation != nil && *s.Relation == ref.Relation && s.Type == ref.Group
}

type structuralResult struct {
	ok         bool
	candidates []SubjectRef
	code       string
	message    string
}

// structural: 형태(식별자/금지문자) + 배정 가능성 검사. 매칭 SubjectRef 후보를 함께 돌려준다.
func structural(model *ModelIR, subject GrantSubject, relation string, resource ResourceRef) structuralResult {
	failS := func(code, message string) structuralResult {
		return structuralResult{ok: false, code: code, message: message}
	}
	if !isIdent(subject.Type) {
		return failS("malformed_request", `invalid subject.type "`+subject.Type+`"`)
	}
	if subject.ID == "" || forbiddenInID.MatchString(subject.ID) {
		return failS("malformed_request", `invalid subject.id "`+subject.ID+`"`)
	}
	if subject.Relation != nil && !isIdent(*subject.Relation) {
		return failS("malformed_request", `invalid subject.relation "`+*subject.Relation+`"`)
	}
	if !isIdent(relation) {
		return failS("malformed_request", `invalid relation "`+relation+`"`)
	}
	if !isIdent(resource.Type) {
		return failS("malformed_request", `invalid resource.type "`+resource.Type+`"`)
	}
	if resource.ID == "" || forbiddenInID.MatchString(resource.ID) {
		return failS("malformed_request", `invalid resource.id "`+resource.ID+`"`)
	}

	allowed, found := assignableSubjects(model, resource.Type, relation)
	if !found {
		return failS("relation_not_assignable",
			`relation "`+relation+`" is not a directly-assignable role/membership on type "`+resource.Type+`"`)
	}
	var candidates []SubjectRef
	for _, ref := range allowed {
		if subjectMatchesRef(ref, subject) {
			candidates = append(candidates, ref)
		}
	}
	if len(candidates) == 0 {
		return failS("subject_type_not_allowed",
			`subject "`+SubjectToUser(subject)+`" type is not allowed for relation "`+relation+`" on "`+resource.Type+`"`)
	}
	return structuralResult{ok: true, candidates: candidates}
}

// ValidateGrant: 구조(배정 가능성) + 조건 규칙(LFGA-14).
func ValidateGrant(model *ModelIR, req *GrantRequest) GrantValidation {
	s := structural(model, req.Subject, req.Relation, req.Resource)
	if !s.ok {
		return failValidation(s.code, s.message)
	}

	if req.Condition != nil {
		name := req.Condition.Name
		if !isIdent(name) {
			return failValidation("malformed_request", `invalid condition name "`+name+`"`)
		}
		known := false
		for _, c := range model.Conditions {
			if c.Name == name {
				known = true
				break
			}
		}
		if !known {
			return failValidation("unknown_condition", `unknown condition "`+name+`"`)
		}
		permitted := false
		for _, ref := range s.candidates {
			if ref.Condition != nil && *ref.Condition == name {
				permitted = true
				break
			}
		}
		if !permitted {
			return failValidation("condition_not_permitted",
				`condition "`+name+`" is not attached to relation "`+req.Relation+`" for this subject type`)
		}
		return okValidation()
	}

	hasConditionless := false
	for _, ref := range s.candidates {
		if ref.Condition == nil {
			hasConditionless = true
			break
		}
	}
	if !hasConditionless {
		return failValidation("condition_required",
			`relation "`+req.Relation+`" requires a condition for this subject type`)
	}
	return okValidation()
}

// ValidateRevoke: 구조만(삭제는 조건 무관).
func ValidateRevoke(model *ModelIR, req *RevokeRequest) GrantValidation {
	s := structural(model, req.Subject, req.Relation, req.Resource)
	if !s.ok {
		return failValidation(s.code, s.message)
	}
	return okValidation()
}

// IsAssignableRelation: (type, relation)이 직접 배정 가능 relation인가(role 또는 group member).
func IsAssignableRelation(model *ModelIR, typ, relation string) bool {
	_, ok := assignableSubjects(model, typ, relation)
	return ok
}

// ParseResourceRef: `type:id` 파싱(엄격). 실패 시 ok=false.
func ParseResourceRef(s string) (ResourceRef, bool) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return ResourceRef{}, false
	}
	typ := s[:i]
	id := s[i+1:]
	if !isIdent(typ) || id == "" || forbiddenInID.MatchString(id) {
		return ResourceRef{}, false
	}
	return ResourceRef{Type: typ, ID: id}, true
}

// ParseGrantSubject: `type:id` 또는 `type:id#relation`(userset) 파싱(엄격). 실패 시 ok=false.
func ParseGrantSubject(s string) (GrantSubject, bool) {
	if hash := strings.Index(s, "#"); hash >= 0 {
		obj, ok := ParseResourceRef(s[:hash])
		relation := s[hash+1:]
		if !ok || !isIdent(relation) {
			return GrantSubject{}, false
		}
		return GrantSubject{Type: obj.Type, ID: obj.ID, Relation: &relation}, true
	}
	obj, ok := ParseResourceRef(s)
	if !ok {
		return GrantSubject{}, false
	}
	return GrantSubject{Type: obj.Type, ID: obj.ID}, true
}
