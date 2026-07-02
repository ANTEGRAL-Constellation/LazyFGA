package contract

import "testing"

func hasCode(errs []ConditionError, code string) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

// isCidr 분기 커버(ValidateConditionDef 경유).
func TestValidateConditionCIDRBranches(t *testing.T) {
	mk := func(cidr string) *ConditionDef {
		return &ConditionDef{Name: "c", Params: []ConditionParam{{Name: "ip", Type: "ipaddress"}},
			Tree: LeafNode(ConditionLeaf{Kind: "ip", Param: "ip", Op: "in_cidr", Cidr: cidr})}
	}
	bad := []string{
		"10.0.0.0",                       // slash 없음
		"10.0.0.0/x",                     // prefix 비숫자
		"10.0.0/8",                       // octet 3개
		"10.0.0.256/8",                   // octet > 255
		"10.0.0.0/33",                    // prefix > 32
		"gggg::/32",                      // ipv6 잘못된 문자
		"2001:db8::/129",                 // ipv6 prefix > 128
		"10.0.0.0/999999999999999999999", // prefix atoi overflow
		"10.0.0.a/8",                     // octet 비숫자
	}
	for _, c := range bad {
		if !hasCode(ValidateConditionDef(mk(c)), "BAD_CIDR") {
			t.Fatalf("expected BAD_CIDR for %q", c)
		}
	}
	good := []string{"192.168.1.0/24", "2001:db8::/32", "::ffff:192.168.0.0/96", "0.0.0.0/0", "255.255.255.255/32"}
	for _, c := range good {
		if hasCode(ValidateConditionDef(mk(c)), "BAD_CIDR") {
			t.Fatalf("unexpected BAD_CIDR for %q", c)
		}
	}
}

// jsStringifyValue: string 파라미터에 number/bool 값을 넣으면 TYPE_MISMATCH 메시지에 각 표현이 들어간다.
func TestTypeMismatchMessageStringifiesValue(t *testing.T) {
	check := func(v ConditionValue, wantMsg string) {
		def := &ConditionDef{Name: "c", Params: []ConditionParam{{Name: "s", Type: "string"}},
			Tree: LeafNode(ConditionLeaf{Kind: "value", Param: "s", Op: "eq", Value: &v})}
		errs := ValidateConditionDef(def)
		found := false
		for _, e := range errs {
			if e.Code == "TYPE_MISMATCH" && e.Path == "tree.value" && e.Message == wantMsg {
				found = true
			}
		}
		if !found {
			t.Fatalf("value %+v: want message %q; errs=%v", v, wantMsg, errs)
		}
	}
	check(NumberValue(42), `value 42 does not match param type string`)
	check(NumberValue(1e21), `value 1e+21 does not match param type string`)
	check(BoolValue(true), `value true does not match param type string`)
	check(BoolValue(false), `value false does not match param type string`)
}

// double 파라미터: 유한수면 통과(defensive isFinite 분기).
func TestValidateConditionDoubleFinite(t *testing.T) {
	def := &ConditionDef{Name: "c", Params: []ConditionParam{{Name: "d", Type: "double"}},
		Tree: LeafNode(ConditionLeaf{Kind: "value", Param: "d", Op: "gt", Value: ptr(NumberValue(1.5))})}
	if len(ValidateConditionDef(def)) != 0 {
		t.Fatalf("expected valid double")
	}
}

// structural(grant) malformed 분기: subject.type / subject.relation / resource.type 비식별자.
func TestGrantStructuralMalformed(t *testing.T) {
	model := &ModelIR{SchemaVersion: "1.1", Resources: []ResourceType{{
		Name:        "document",
		Roles:       []Role{{Name: "editor", AssignableBy: []SubjectRef{{Kind: "user"}}}},
		Permissions: []Permission{{Name: "read", GrantedByRoles: []string{"editor"}}},
	}}}
	badRel := "bad rel"
	cases := []struct {
		name string
		req  GrantRequest
	}{
		{"bad-subject-type", GrantRequest{Subject: GrantSubject{Type: "bad type", ID: "x"}, Relation: "editor", Resource: ResourceRef{Type: "document", ID: "d"}}},
		{"bad-subject-relation", GrantRequest{Subject: GrantSubject{Type: "team", ID: "eng", Relation: &badRel}, Relation: "editor", Resource: ResourceRef{Type: "document", ID: "d"}}},
		{"bad-resource-type", GrantRequest{Subject: GrantSubject{Type: "user", ID: "x"}, Relation: "editor", Resource: ResourceRef{Type: "bad type", ID: "d"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ValidateGrant(model, &tc.req)
			if r.OK || r.Code != "malformed_request" {
				t.Fatalf("got %+v, want malformed_request", r)
			}
		})
	}
}

// SubjectToUser: 빈 문자열 relation은 bare user로 취급(TS truthiness).
func TestSubjectToUserEmptyRelation(t *testing.T) {
	empty := ""
	if got := SubjectToUser(GrantSubject{Type: "user", ID: "a", Relation: &empty}); got != "user:a" {
		t.Fatalf("got %q, want user:a", got)
	}
}
