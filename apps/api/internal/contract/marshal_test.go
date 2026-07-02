package contract

import (
	"encoding/json"
	"testing"
)

// 커스텀 (un)marshaler의 에러/방어 분기와 생성 헬퍼를 좁게 커버한다(정상 경로는 corpus에서).

func mustMarshalErr(t *testing.T, v any) {
	t.Helper()
	if _, err := json.Marshal(v); err == nil {
		t.Fatalf("expected marshal error for %#v", v)
	}
}

func mustUnmarshalErr(t *testing.T, data string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(data), v); err == nil {
		t.Fatalf("expected unmarshal error for %q", data)
	}
}

func TestSubjectRefMarshalErrors(t *testing.T) {
	mustMarshalErr(t, SubjectRef{Kind: "bogus"})
	var s SubjectRef
	mustUnmarshalErr(t, `{"kind":"bogus"}`, &s)
	mustUnmarshalErr(t, `123`, &s)
}

func TestSubjectRefConstructors(t *testing.T) {
	c := "gate"
	if b, _ := json.Marshal(UserRef(&c)); string(b) != `{"kind":"user","condition":"gate"}` {
		t.Fatalf("UserRef = %s", b)
	}
	if b, _ := json.Marshal(GroupRef("team", "member", nil)); string(b) != `{"kind":"group","group":"team","relation":"member"}` {
		t.Fatalf("GroupRef = %s", b)
	}
}

func TestTimeRhsErrors(t *testing.T) {
	mustMarshalErr(t, TimeRhs{Kind: "bogus"})
	var r TimeRhs
	mustUnmarshalErr(t, `{"kind":"bogus"}`, &r)
	mustUnmarshalErr(t, `not-json`, &r)
}

func TestConditionLeafErrors(t *testing.T) {
	mustMarshalErr(t, ConditionLeaf{Kind: "bogus"})
	var l ConditionLeaf
	mustUnmarshalErr(t, `{"kind":"bogus"}`, &l)
	mustUnmarshalErr(t, `123`, &l)
}

// 방어적 deref: time leaf의 Rhs=nil이면 deref가 빈 TimeRhs를 만들고, 그 marshal이 실패한다.
func TestConditionLeafTimeNilRhsMarshalFails(t *testing.T) {
	mustMarshalErr(t, ConditionLeaf{Kind: "time", Param: "t", Op: "lt", Rhs: nil})
}

// 방어적 derefValue: value leaf의 Value=nil이면 빈 ConditionValue(=string "") 로 marshal된다.
func TestConditionLeafValueNilValueMarshals(t *testing.T) {
	b, err := json.Marshal(ConditionLeaf{Kind: "value", Param: "x", Op: "eq", Value: nil})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if string(b) != `{"kind":"value","param":"x","op":"eq","value":""}` {
		t.Fatalf("got %s", b)
	}
}

func TestConditionValueErrors(t *testing.T) {
	mustMarshalErr(t, ConditionValue{Kind: ValueKind(99)})
	var v ConditionValue
	mustUnmarshalErr(t, `null`, &v)
	mustUnmarshalErr(t, ``, &v)
	mustUnmarshalErr(t, `[1]`, &v) // 배열은 string|number|bool 아님.
}

func TestConditionNodeErrors(t *testing.T) {
	mustMarshalErr(t, ConditionNode{}) // 둘 다 nil.
	var n ConditionNode
	mustUnmarshalErr(t, `123`, &n)              // object 아님.
	mustUnmarshalErr(t, `{"children":"x"}`, &n) // children 이 배열 아님 → 내부 group unmarshal 실패.
	mustUnmarshalErr(t, `{"kind":"bogus"}`, &n) // leaf unmarshal 실패.
}

func TestConditionNodeConstructors(t *testing.T) {
	g := GroupNode("or", LeafNode(ConditionLeaf{Kind: "value", Param: "x", Op: "eq", Value: ptr(StringValue("a"))}))
	if !g.isGroup() {
		t.Fatal("GroupNode should be group")
	}
	l := LeafNode(ConditionLeaf{Kind: "ip", Param: "ip", Op: "in_cidr", Cidr: "10.0.0.0/8"})
	if l.isGroup() {
		t.Fatal("LeafNode should not be group")
	}
}

func TestReasonStepErrors(t *testing.T) {
	mustMarshalErr(t, ReasonStep{Via: "bogus"})
	var s ReasonStep
	mustUnmarshalErr(t, `{"via":"bogus"}`, &s)
	mustUnmarshalErr(t, `123`, &s)
}

func TestMissingLinkErrors(t *testing.T) {
	mustMarshalErr(t, MissingLink{Kind: "bogus"})
	var m MissingLink
	mustUnmarshalErr(t, `{"kind":"bogus"}`, &m)
	mustUnmarshalErr(t, `123`, &m)
}

func ptr(v ConditionValue) *ConditionValue { return &v }
