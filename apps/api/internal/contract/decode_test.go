package contract

import (
	"strings"
	"testing"
)

// DecodeModelIR strict shape 디코더의 에러 분기를 좁게 커버한다(정상/일부 위반은 corpus에서).

func hasIssuePath(issues []Issue, path string) bool {
	for _, is := range issues {
		if is.Path == path {
			return true
		}
	}
	return false
}

func TestDecodeModelIRValid(t *testing.T) {
	data := `{"schemaVersion":"1.1","groups":[],"resources":[{"name":"doc","parents":[],"roles":[{"name":"viewer","assignableBy":[{"kind":"user"}]}],"permissions":[{"name":"read","grantedByRoles":["viewer"],"inheritFromParents":[]}]}]}`
	ir, issues := DecodeModelIR([]byte(data))
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	if ir == nil || ir.SchemaVersion != "1.1" || ir.Resources[0].Name != "doc" {
		t.Fatalf("decoded ir wrong: %+v", ir)
	}
}

func TestDecodeModelIRInvalidJSON(t *testing.T) {
	_, issues := DecodeModelIR([]byte(`{not json`))
	if len(issues) != 1 || !strings.HasPrefix(issues[0].Message, "invalid JSON") {
		t.Fatalf("issues = %v", issues)
	}
}

func TestDecodeModelIRShapeErrors(t *testing.T) {
	cases := []struct {
		name     string
		json     string
		wantPath string // optional: 하나 이상의 이슈가 이 path를 가져야 함.
	}{
		{"root-not-object", `42`, ""},
		{"missing-schema", `{"groups":[],"resources":[]}`, "schemaVersion"},
		{"groups-required", `{"schemaVersion":"1.1","resources":[]}`, "groups"},
		{"groups-not-array", `{"schemaVersion":"1.1","groups":"x","resources":[]}`, "groups"},
		{"group-not-object", `{"schemaVersion":"1.1","groups":[1],"resources":[]}`, "groups[0]"},
		{"member-required", `{"schemaVersion":"1.1","groups":[{"name":"g"}],"resources":[]}`, "groups[0].memberTypes"},
		{"member-not-array", `{"schemaVersion":"1.1","groups":[{"name":"g","memberTypes":5}],"resources":[]}`, "groups[0].memberTypes"},
		{"subject-kind-required", `{"schemaVersion":"1.1","groups":[{"name":"g","memberTypes":[{}]}],"resources":[]}`, "groups[0].memberTypes[0].kind"},
		{"subject-condition-type", `{"schemaVersion":"1.1","groups":[{"name":"g","memberTypes":[{"kind":"user","condition":123}]}],"resources":[]}`, "groups[0].memberTypes[0].condition"},
		{"subject-group-missing", `{"schemaVersion":"1.1","groups":[{"name":"g","memberTypes":[{"kind":"group","relation":"member"}]}],"resources":[]}`, "groups[0].memberTypes[0].group"},
		{"subject-relation-literal", `{"schemaVersion":"1.1","groups":[{"name":"g","memberTypes":[{"kind":"group","group":"g","relation":"owner"}]}],"resources":[]}`, "groups[0].memberTypes[0].relation"},
		{"subject-unknown-kind", `{"schemaVersion":"1.1","groups":[{"name":"g","memberTypes":[{"kind":"regex"}]}],"resources":[]}`, "groups[0].memberTypes[0].kind"},
		{"parent-types-string-elem", `{"schemaVersion":"1.1","groups":[],"resources":[{"name":"d","parents":[{"relationName":"p","parentTypes":[1]}],"roles":[],"permissions":[]}]}`, "resources[0].parents[0].parentTypes[0]"},
		{"permission-arrays", `{"schemaVersion":"1.1","groups":[],"resources":[{"name":"d","parents":[],"roles":[],"permissions":[{"name":"read"}]}]}`, "resources[0].permissions[0].grantedByRoles"},
		{"param-type-missing", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"p"}],"tree":{"kind":"value","param":"p","op":"eq","value":1}}]}`, "conditions[0].params[0].type"},
		{"param-type-not-string", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"p","type":5}],"tree":{"kind":"value","param":"p","op":"eq","value":1}}]}`, "conditions[0].params[0].type"},
		{"param-type-invalid", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"p","type":"bogus"}],"tree":{"kind":"value","param":"p","op":"eq","value":1}}]}`, "conditions[0].params[0].type"},
		{"leaf-op-enum", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"t","type":"timestamp"}],"tree":{"kind":"time","param":"t","op":"BAD","rhs":{"kind":"literal","rfc3339":"x"}}}]}`, "conditions[0].tree.op"},
		{"leaf-time-rhs-required", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"t","type":"timestamp"}],"tree":{"kind":"time","param":"t","op":"lt"}}]}`, "conditions[0].tree.rhs"},
		{"timerhs-unknown-kind", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"t","type":"timestamp"}],"tree":{"kind":"time","param":"t","op":"lt","rhs":{"kind":"bogus"}}}]}`, "conditions[0].tree.rhs.kind"},
		{"timerhs-literal-missing", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"t","type":"timestamp"}],"tree":{"kind":"time","param":"t","op":"lt","rhs":{"kind":"literal"}}}]}`, "conditions[0].tree.rhs.rfc3339"},
		{"timerhs-not-object", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"t","type":"timestamp"}],"tree":{"kind":"time","param":"t","op":"lt","rhs":9}}]}`, "conditions[0].tree.rhs"},
		{"leaf-ip-op-literal", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"ip","type":"ipaddress"}],"tree":{"kind":"ip","param":"ip","op":"bad","cidr":"10.0.0.0/8"}}]}`, "conditions[0].tree.op"},
		{"leaf-value-nonprimitive", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"p","type":"string"}],"tree":{"kind":"value","param":"p","op":"eq","value":{}}}]}`, "conditions[0].tree.value"},
		{"leaf-value-missing", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[{"name":"p","type":"string"}],"tree":{"kind":"value","param":"p","op":"eq"}}]}`, "conditions[0].tree.value"},
		{"leaf-kind-missing", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[],"tree":{"param":"p","op":"eq"}}]}`, "conditions[0].tree.kind"},
		{"leaf-kind-unknown", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[],"tree":{"kind":"regex","param":"p"}}]}`, "conditions[0].tree.kind"},
		{"node-group-op-bad", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[],"tree":{"op":"xor","children":[]}}]}`, "conditions[0].tree.op"},
		{"condition-tree-required", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":[]}]}`, "conditions[0].tree"},
		{"condition-params-not-array", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":[{"name":"c","params":5,"tree":{"kind":"value","param":"p","op":"eq","value":1}}]}`, "conditions[0].params"},
		{"conditions-not-array", `{"schemaVersion":"1.1","groups":[],"resources":[],"conditions":5}`, "conditions"},
		{"role-not-object", `{"schemaVersion":"1.1","groups":[],"resources":[{"name":"d","parents":[],"roles":[7],"permissions":[]}]}`, "resources[0].roles[0]"},
		{"parent-not-object", `{"schemaVersion":"1.1","groups":[],"resources":[{"name":"d","parents":[7],"roles":[],"permissions":[]}]}`, "resources[0].parents[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ir, issues := DecodeModelIR([]byte(tc.json))
			if len(issues) == 0 {
				t.Fatalf("expected issues, got none")
			}
			if ir != nil {
				t.Fatalf("expected nil ir on invalid shape")
			}
			if tc.wantPath != "" && !hasIssuePath(issues, tc.wantPath) {
				t.Fatalf("no issue at path %q; issues=%v", tc.wantPath, issues)
			}
		})
	}
}
