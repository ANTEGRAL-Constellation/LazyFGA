package pdp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/openfga"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
)

// fakeGW는 설정 가능한 가짜 OpenFGA다(reason.test.ts fakeDeps 포팅).
type fakeGW struct {
	allow    func(rel, obj string) bool
	tuples   func(obj, rel string) []string
	checkErr error
	readErr  error
	// 캡처: 모델 핀/컨텍스트 전달 검증용(포인터로 공유).
	pins *[]string
	ctxs *[]map[string]any
}

func (f fakeGW) Check(_ context.Context, in openfga.CheckInput, opts ...openfga.CheckOption) (bool, error) {
	if f.pins != nil {
		*f.pins = append(*f.pins, openfga.ResolveCheckAuthorizationModelID(opts...))
	}
	if f.ctxs != nil {
		*f.ctxs = append(*f.ctxs, in.Context)
	}
	if f.checkErr != nil {
		return false, f.checkErr
	}
	return f.allow(in.Relation, in.Object), nil
}

func (f fakeGW) Read(_ context.Context, in openfga.ReadInput) ([]openfga.ReadTuple, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	obj, rel := "", ""
	if in.Object != nil {
		obj = *in.Object
	}
	if in.Relation != nil {
		rel = *in.Relation
	}
	var users []string
	if f.tuples != nil {
		users = f.tuples(obj, rel)
	}
	out := make([]openfga.ReadTuple, 0, len(users))
	for _, u := range users {
		out = append(out, openfga.ReadTuple{User: u, Relation: rel, Object: obj})
	}
	return out, nil
}

func loadIR(t *testing.T) *contract.ModelIR {
	t.Helper()
	data, err := os.ReadFile(testutil.RepoPath("packages", "shared", "src", "__fixtures__", "doc-folder-team.ir.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var ir contract.ModelIR
	if err := json.Unmarshal(data, &ir); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &ir
}

func pinOf(ir *contract.ModelIR, decision bool) reasonPin {
	return reasonPin{decision: decision, authorizationModelID: "m1", ir: ir}
}

func TestExplain_directRole(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{
		allow: func(rel, obj string) bool { return rel == "viewer" && obj == "document:123" },
		tuples: func(obj, rel string) []string {
			if obj == "document:123" && rel == "viewer" {
				return []string{"user:alice"}
			}
			return nil
		},
	}
	r, err := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Decision || r.Truncated != nil {
		t.Fatalf("decision=%v truncated=%v", r.Decision, r.Truncated)
	}
	if len(r.Path) != 1 || r.Path[0].Via != "role" || r.Path[0].Role != "viewer" || r.Path[0].On != "document" || !r.Path[0].Direct {
		t.Fatalf("path=%+v", r.Path)
	}
}

func TestExplain_viaGroupMembership(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{
		allow: func(rel, obj string) bool {
			return (rel == "viewer" && obj == "document:123") || (rel == "member" && obj == "team:eng")
		},
		tuples: func(obj, rel string) []string {
			if obj == "document:123" && rel == "viewer" {
				return []string{"team:eng#member"}
			}
			return nil
		},
	}
	r, err := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Truncated != nil {
		t.Fatalf("should not be truncated: %+v", r)
	}
	s := r.Path[0]
	if s.Direct || s.Group == nil || *s.Group != "team" || s.GroupObject == nil || *s.GroupObject != "team:eng" {
		t.Fatalf("step=%+v", s)
	}
	// 텍스트: group membership 표기.
	if got := describePath("user:alice", "read", "document:123", r.Path); got != "user:alice can read document:123: role viewer (via team:eng membership)" {
		t.Fatalf("text=%q", got)
	}
}

func TestExplain_wildcardTruncated(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{
		allow: func(rel, obj string) bool { return rel == "viewer" && obj == "document:123" },
		tuples: func(obj, rel string) []string {
			if obj == "document:123" && rel == "viewer" {
				return []string{"user:*"}
			}
			return nil
		},
	}
	r, _ := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil)
	if !r.Decision || r.Truncated == nil || !*r.Truncated {
		t.Fatalf("expected truncated allow: %+v", r)
	}
	if len(r.Path) != 1 {
		t.Fatalf("path=%+v", r.Path)
	}
}

func TestExplain_inheritedViaParent(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{
		allow: func(rel, obj string) bool { return rel == "viewer" && obj == "folder:1" },
		tuples: func(obj, rel string) []string {
			if obj == "document:123" && rel == "parent" {
				return []string{"folder:1"}
			}
			if obj == "folder:1" && rel == "viewer" {
				return []string{"user:alice"}
			}
			return nil
		},
	}
	r, _ := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil)
	if r.Truncated != nil || len(r.Path) != 2 {
		t.Fatalf("path=%+v truncated=%v", r.Path, r.Truncated)
	}
	if r.Path[0].Via != "parent" || r.Path[0].Relation != "parent" || r.Path[0].Parent != "folder" || r.Path[0].ParentObject == nil || *r.Path[0].ParentObject != "folder:1" {
		t.Fatalf("step0=%+v", r.Path[0])
	}
	if r.Path[1].Via != "role" || r.Path[1].Role != "viewer" || r.Path[1].On != "folder" || !r.Path[1].Direct {
		t.Fatalf("step1=%+v", r.Path[1])
	}
	if got := describePath("user:alice", "read", "document:123", r.Path); got != "user:alice can read document:123: inherited via parent from folder:1 → role viewer (direct)" {
		t.Fatalf("text=%q", got)
	}
}

func TestExplain_denyMissingLinks(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{allow: func(string, string) bool { return false }}
	r, _ := explain(context.Background(), deps, pinOf(ir, false), "user:bob", "read", "document:123", nil)
	if r.Decision {
		t.Fatal("should be denied")
	}
	foundRole, foundParent := false, false
	for _, l := range r.MissingLinks {
		if l.Kind == "role" && l.On == "document" && len(l.AnyOf) == 3 {
			foundRole = true
		}
		if l.Kind == "parent" && l.Relation == "parent" && l.Needs == "can_read" {
			foundParent = true
		}
	}
	if !foundRole || !foundParent {
		t.Fatalf("missingLinks=%+v", r.MissingLinks)
	}
	want := "denied: needs one of [viewer, editor, owner] on document:123, or can_read via parent (parent)"
	if r.Text != want {
		t.Fatalf("text=%q want=%q", r.Text, want)
	}
}

func TestExplain_selfReferentialCycle(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{
		allow: func(string, string) bool { return false }, // 어떤 role grant도 없음.
		tuples: func(obj, rel string) []string {
			if rel == "parent" && obj == "document:123" {
				return []string{"document:123"}
			}
			return nil
		},
	}
	// decision=true(상위 Check)지만 witness 재구성 불가 → 정직한 truncated fallback, 무한루프 없음.
	r, _ := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil)
	if !r.Decision || r.Truncated == nil || !*r.Truncated || r.Path != nil {
		t.Fatalf("expected truncated fallback: %+v", r)
	}
	if r.Text != "allowed via can_read (path reconstruction incomplete)" {
		t.Fatalf("text=%q", r.Text)
	}
}

func TestExplain_denyEmptyLinksSerializesArray(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{allow: func(string, string) bool { return false }}
	// object 타입이 IR에 없음 → denyLinks가 빈 슬라이스 → "missingLinks":[]로 직렬화.
	r, _ := explain(context.Background(), deps, pinOf(ir, false), "user:bob", "read", "ghost:1", nil)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"decision":false,"missingLinks":[],"text":"denied: needs a grant that does not exist in the model"}` {
		t.Fatalf("json=%s", got)
	}
}

func TestExplain_partialSuffix(t *testing.T) {
	ir := loadIR(t)
	// viewer allow지만 tuple read가 alice/그룹을 못 밝힘 → incomplete → path + (partial).
	deps := fakeGW{
		allow:  func(rel, obj string) bool { return rel == "viewer" && obj == "document:123" },
		tuples: func(obj, rel string) []string { return []string{"user:someoneelse"} },
	}
	r, _ := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil)
	if r.Truncated == nil || !*r.Truncated {
		t.Fatalf("expected truncated: %+v", r)
	}
	if r.Text == "" || r.Text[len(r.Text)-9:] != "(partial)" {
		t.Fatalf("text should end with (partial): %q", r.Text)
	}
}

func TestExplain_checkError(t *testing.T) {
	ir := loadIR(t)
	deps := fakeGW{checkErr: errors.New("check boom")}
	if _, err := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil); err == nil {
		t.Fatal("expected check error to propagate")
	}
}

func TestExplain_readErrorInClassify(t *testing.T) {
	ir := loadIR(t)
	// role check allow → classifyRoleStep이 Read 호출 → Read 에러 전파.
	deps := fakeGW{allow: func(string, string) bool { return true }, readErr: errors.New("read boom")}
	if _, err := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil); err == nil {
		t.Fatal("expected read error to propagate")
	}
}

func TestExplain_readErrorInParentLoop(t *testing.T) {
	ir := loadIR(t)
	// role은 모두 deny, parent read에서 에러.
	deps := fakeGW{
		allow:   func(rel, obj string) bool { return false },
		readErr: errors.New("read boom"),
	}
	// document.read는 inheritFromParents=[parent]라 parent read를 시도한다.
	if _, err := explain(context.Background(), deps, pinOf(ir, true), "user:alice", "read", "document:123", nil); err == nil {
		t.Fatal("expected parent read error to propagate")
	}
}

func TestSplitObject(t *testing.T) {
	if _, _, ok := splitObject("nocolon"); ok {
		t.Error("no colon should be !ok")
	}
	typ, id, ok := splitObject("team:eng")
	if !ok || typ != "team" || id != "eng" {
		t.Fatalf("got %q %q %v", typ, id, ok)
	}
}

func TestDescribeMissing_emptyLinks(t *testing.T) {
	if got := describeMissing("doc:1", nil); got != "denied: needs a grant that does not exist in the model" {
		t.Fatalf("got %q", got)
	}
}
