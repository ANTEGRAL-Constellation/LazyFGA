package model

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
)

// loadDocFolderTeamIR는 diff/reason 테스트가 공유하는 fixture(docFolderTeamIR)를 읽어 온다.
func loadDocFolderTeamIR(t *testing.T) *contract.ModelIR {
	t.Helper()
	p := testutil.RepoPath("packages", "shared", "src", "__fixtures__", "doc-folder-team.ir.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var ir contract.ModelIR
	if err := json.Unmarshal(data, &ir); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &ir
}

func changeJSON(t *testing.T, c DiffChange) string {
	t.Helper()
	b, err := c.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal change: %v", err)
	}
	return string(b)
}

func containsChange(t *testing.T, changes []DiffChange, want DiffChange) bool {
	t.Helper()
	wj := changeJSON(t, want)
	for _, c := range changes {
		if changeJSON(t, c) == wj {
			return true
		}
	}
	return false
}

func TestDiffModels_identicalEmpty(t *testing.T) {
	ir := loadDocFolderTeamIR(t)
	if got := DiffModels(ir, ir); len(got) != 0 {
		t.Fatalf("identical models should diff empty, got %+v", got)
	}
}

func TestDiffModels_roleAddedAndGrantChanged(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	to := loadDocFolderTeamIR(t)
	// document(resources[1])에 auditor role 추가 + read.grantedByRoles에 auditor 추가.
	to.Resources[1].Roles = append(to.Resources[1].Roles, contract.Role{Name: "auditor", AssignableBy: []contract.SubjectRef{contract.UserRef(nil)}})
	to.Resources[1].Permissions[0].GrantedByRoles = append(to.Resources[1].Permissions[0].GrantedByRoles, "auditor")
	changes := DiffModels(base, to)
	if !containsChange(t, changes, DiffChange{Kind: "ROLE_ADDED", Type: "document", Role: "auditor"}) {
		t.Errorf("missing ROLE_ADDED: %+v", changes)
	}
	found := false
	for _, c := range changes {
		if c.Kind == "GRANT_CHANGED" && c.Type == "document" {
			for _, a := range c.Added {
				if a == "auditor" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("missing GRANT_CHANGED with auditor: %+v", changes)
	}
}

func TestDiffModels_typeRemoved(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	to := loadDocFolderTeamIR(t)
	// folder(resources[0]) 제거. document는 folder 참조가 남아 diff에 여러 change가 나올 수 있으나
	// TYPE_REMOVED folder는 반드시 있어야 한다.
	to.Resources = to.Resources[1:]
	if !containsChange(t, DiffModels(base, to), DiffChange{Kind: "TYPE_REMOVED", Type: "folder"}) {
		t.Errorf("missing TYPE_REMOVED folder")
	}
}

func TestDiffModels_typeAdded(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	to := loadDocFolderTeamIR(t)
	to.Resources = append(to.Resources, contract.ResourceType{
		Name:        "project",
		Parents:     []contract.ParentRef{},
		Roles:       []contract.Role{{Name: "owner", AssignableBy: []contract.SubjectRef{contract.UserRef(nil)}}},
		Permissions: []contract.Permission{{Name: "read", GrantedByRoles: []string{"owner"}, InheritFromParents: []string{}}},
	})
	if !containsChange(t, DiffModels(base, to), DiffChange{Kind: "TYPE_ADDED", Type: "project"}) {
		t.Errorf("missing TYPE_ADDED project")
	}
}

func TestDiffModels_parentAddedAndRemoved(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	// PARENT_ADDED: folder(resources[0])에 parent 추가.
	to := loadDocFolderTeamIR(t)
	to.Resources[0].Parents = append(to.Resources[0].Parents, contract.ParentRef{RelationName: "parent", ParentTypes: []string{"folder"}})
	if !containsChange(t, DiffModels(base, to), DiffChange{Kind: "PARENT_ADDED", Type: "folder", RelationName: "parent", ParentType: "folder"}) {
		t.Errorf("missing PARENT_ADDED")
	}
	// PARENT_REMOVED: document(resources[1])의 parent 제거.
	to2 := loadDocFolderTeamIR(t)
	to2.Resources[1].Parents = []contract.ParentRef{}
	to2.Resources[1].Permissions[0].InheritFromParents = []string{} // 상속 참조도 제거(무결).
	if !containsChange(t, DiffModels(base, to2), DiffChange{Kind: "PARENT_REMOVED", Type: "document", RelationName: "parent", ParentType: "folder"}) {
		t.Errorf("missing PARENT_REMOVED")
	}
}

func TestDiffModels_roleAssignableChanged(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	to := loadDocFolderTeamIR(t)
	to.Resources[1].Roles[1].AssignableBy = []contract.SubjectRef{contract.UserRef(nil)} // editor: team#member 제거.
	if !containsChange(t, DiffModels(base, to), DiffChange{
		Kind: "ROLE_ASSIGNABLE_CHANGED", Type: "document", Role: "editor",
		Added: []string{}, Removed: []string{"team#member"},
	}) {
		t.Errorf("missing ROLE_ASSIGNABLE_CHANGED")
	}
}

func TestDiffModels_roleRemoved(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	to := loadDocFolderTeamIR(t)
	// document owner role 제거 + read.grantedByRoles에서 owner 제거(무결).
	to.Resources[1].Roles = to.Resources[1].Roles[1:] // owner 제거.
	to.Resources[1].Permissions[0].GrantedByRoles = []string{"viewer", "editor"}
	if !containsChange(t, DiffModels(base, to), DiffChange{Kind: "ROLE_REMOVED", Type: "document", Role: "owner"}) {
		t.Errorf("missing ROLE_REMOVED owner")
	}
}

func TestDiffModels_permissionAddedRemovedInherit(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	// PERMISSION_ADDED: document에 write permission 추가.
	to := loadDocFolderTeamIR(t)
	to.Resources[1].Permissions = append(to.Resources[1].Permissions, contract.Permission{Name: "write", GrantedByRoles: []string{"owner"}, InheritFromParents: []string{}})
	if !containsChange(t, DiffModels(base, to), DiffChange{Kind: "PERMISSION_ADDED", Type: "document", Permission: "write"}) {
		t.Errorf("missing PERMISSION_ADDED write")
	}
	// PERMISSION_REMOVED: folder에 write 추가된 base 대비 to에서 없앰.
	baseW := loadDocFolderTeamIR(t)
	baseW.Resources[0].Permissions = append(baseW.Resources[0].Permissions, contract.Permission{Name: "write", GrantedByRoles: []string{"owner"}, InheritFromParents: []string{}})
	toW := loadDocFolderTeamIR(t)
	if !containsChange(t, DiffModels(baseW, toW), DiffChange{Kind: "PERMISSION_REMOVED", Type: "folder", Permission: "write"}) {
		t.Errorf("missing PERMISSION_REMOVED write")
	}
	// PERMISSION_INHERIT_CHANGED: document.read inheritFromParents 비움.
	toI := loadDocFolderTeamIR(t)
	toI.Resources[1].Permissions[0].InheritFromParents = []string{}
	if !containsChange(t, DiffModels(base, toI), DiffChange{
		Kind: "PERMISSION_INHERIT_CHANGED", Type: "document", Permission: "read",
		Added: []string{}, Removed: []string{"parent"},
	}) {
		t.Errorf("missing PERMISSION_INHERIT_CHANGED")
	}
}

func TestDiffModels_deterministicOrdering(t *testing.T) {
	base := loadDocFolderTeamIR(t)
	to := loadDocFolderTeamIR(t)
	to.Resources[1].Roles = append(to.Resources[1].Roles, contract.Role{Name: "auditor", AssignableBy: []contract.SubjectRef{contract.UserRef(nil)}})
	a, _ := json.Marshal(DiffModels(base, to))
	b, _ := json.Marshal(DiffModels(base, to))
	if string(a) != string(b) {
		t.Fatalf("non-deterministic ordering:\n%s\n%s", a, b)
	}
}

func TestDiffChange_marshalKinds(t *testing.T) {
	cases := []struct {
		c    DiffChange
		want string
	}{
		{DiffChange{Kind: "TYPE_ADDED", Type: "x"}, `{"kind":"TYPE_ADDED","type":"x"}`},
		{DiffChange{Kind: "ROLE_ADDED", Type: "x", Role: "r"}, `{"kind":"ROLE_ADDED","type":"x","role":"r"}`},
		{DiffChange{Kind: "ROLE_ASSIGNABLE_CHANGED", Type: "x", Role: "r", Added: []string{"a"}, Removed: []string{}}, `{"kind":"ROLE_ASSIGNABLE_CHANGED","type":"x","role":"r","added":["a"],"removed":[]}`},
		{DiffChange{Kind: "PERMISSION_ADDED", Type: "x", Permission: "p"}, `{"kind":"PERMISSION_ADDED","type":"x","permission":"p"}`},
		{DiffChange{Kind: "GRANT_CHANGED", Type: "x", Permission: "p", Added: []string{}, Removed: []string{"r"}}, `{"kind":"GRANT_CHANGED","type":"x","permission":"p","added":[],"removed":["r"]}`},
		{DiffChange{Kind: "PARENT_ADDED", Type: "x", RelationName: "parent", ParentType: "folder"}, `{"kind":"PARENT_ADDED","type":"x","relationName":"parent","parentType":"folder"}`},
	}
	for _, tc := range cases {
		if got := changeJSON(t, tc.c); got != tc.want {
			t.Errorf("marshal %s = %s, want %s", tc.c.Kind, got, tc.want)
		}
	}
	if _, err := (DiffChange{Kind: "BOGUS"}).MarshalJSON(); err == nil {
		t.Error("invalid kind should error")
	}
}
