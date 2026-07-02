// Package idp는 IdP webhook 프레임워크를 구현한다(LFGA-26, TS modules/idp/* 포팅).
// 선언적 서명 검증(signature.go) · 이벤트 추출(extraction.go) · in-repo preset(presets.go) ·
// 매핑 엔진(mapping.go) · 저장소(repo.go) · 라우트(routes.go)로 구성된다. provider-agnostic.
package idp

import (
	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

// IdpEvent는 provider 독립 정규 이벤트다. extraction 엔진이 raw payload를 이 형태로 정규화한다.
type IdpEvent struct {
	// Type은 정규 이벤트 타입. 예: "user.grant.added".
	Type string
	// Subject는 영향받는 주체. ID는 OpenFGA user id로 쓰인다.
	Subject Subject
	// Attributes 값은 string(스칼라) 또는 []string(fan-out 소스)이다.
	// 예: { project: "123", roleKeys: ["a","b"] }.
	Attributes map[string]any
}

// Subject는 정규 이벤트의 주체다. Type은 추출 규칙이 정한 주체 타입(예: "user").
type Subject struct {
	Type string
	ID   string
}

// MatchPredicate는 동등 비교 술어다. 이벤트의 Field 경로 값이 Equals와 같아야 매칭.
type MatchPredicate struct {
	Field  string `json:"field"` // "type" | "subject" | "attributes.<k>"
	Equals string `json:"equals"`
}

// TupleTemplate는 tuple 템플릿이다. 각 문자열은 {{path}} placeholder를 이벤트 값으로 치환한다.
type TupleTemplate struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// MarshalJSON은 TS 응답과 바이트 동일하게 Postgres jsonb 정규화 키 순서(user, object,
// relation — 길이→바이트순)로 직렬화한다. TS는 jsonb 컬럼에서 읽은 객체를 그대로 에코하므로
// 응답 키 순서가 항상 이 순서다(LFGA-26 리뷰 CRITICAL 반영).
func (t TupleTemplate) MarshalJSON() ([]byte, error) {
	return jsutil.MarshalJSON(struct {
		User     string `json:"user"`
		Object   string `json:"object"`
		Relation string `json:"relation"`
	}{t.User, t.Object, t.Relation})
}

// MappingRule은 매핑 규칙(설정형)이다. idp_mapping_rule 행과 1:1.
type MappingRule struct {
	EventType     string
	Match         []MatchPredicate
	TupleTemplate TupleTemplate
	Op            string // "write" | "delete"
	Priority      int
	// FanOut 지정 시 그 이름의 배열 attribute를 원소별 1 tuple로 펼친다({{item}} 바인딩).
	// nil이면 단일 tuple(기존 동작).
	FanOut *string
}
