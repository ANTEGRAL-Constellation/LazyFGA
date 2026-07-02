package contract

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

// validateConditionDef 포트(LFGA-13/14). code/path/message는 TS와 바이트 동일.

var (
	rfc3339RE  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$`)
	digitsRE   = regexp.MustCompile(`^\d+$`)
	ipv6RE     = regexp.MustCompile(`^[0-9a-fA-F:.]+$`)
	octetRE    = regexp.MustCompile(`^\d{1,3}$`)
	maxSafeInt = 9007199254740991.0 // 2^53 - 1
)

var valueTypes = map[string]struct{}{"string": {}, "int": {}, "double": {}, "bool": {}}
var orderOps = map[string]struct{}{"lt": {}, "lte": {}, "gt": {}, "gte": {}}

// badIdent: 식별자 규칙 + DSL/CEL 예약어 금지(condition/param 이름은 CEL로 흘러감).
func badIdent(name string) bool {
	return !isIdent(name) || isReservedWord(name) || isCelReserved(name)
}

// isCidr: IPv4/IPv6 CIDR 형식(보수적) 검사.
func isCidr(s string) bool {
	slash := strings.LastIndex(s, "/")
	if slash < 0 {
		return false
	}
	addr := s[:slash]
	prefixStr := s[slash+1:]
	if !digitsRE.MatchString(prefixStr) {
		return false
	}
	prefix, err := strconv.Atoi(prefixStr)
	if err != nil { // Number(prefixStr) 가 매우 커도 아래 범위 검사에서 탈락.
		return false
	}
	if strings.Contains(addr, ":") {
		return prefix >= 0 && prefix <= 128 && ipv6RE.MatchString(addr) && len(addr) > 0
	}
	octets := strings.Split(addr, ".")
	if len(octets) != 4 {
		return false
	}
	for _, o := range octets {
		if !octetRE.MatchString(o) {
			return false
		}
		n, _ := strconv.Atoi(o)
		if n > 255 {
			return false
		}
	}
	return prefix >= 0 && prefix <= 32
}

// jsStringifyValue: TS `JSON.stringify(v)` 재현(메시지용).
func jsStringifyValue(v ConditionValue) string {
	switch v.Kind {
	case ValueString:
		return jsutil.JSONString(v.Str)
	case ValueNumber:
		return jsutil.NumberString(v.Num)
	case ValueBool:
		if v.Bool {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// isSafeInteger: JS Number.isSafeInteger(v) 재현(v는 JSON float64).
func isSafeInteger(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v) && math.Abs(v) <= maxSafeInt
}

// ValidateConditionDef는 조건 정의를 정적 검증한다(빈 슬라이스 = 유효, 패닉 없음).
func ValidateConditionDef(def *ConditionDef) []ConditionError {
	var errs []ConditionError
	add := func(code, path, message string) {
		errs = append(errs, ConditionError{Code: code, Path: path, Message: message})
	}

	// rule 1: 조건 이름.
	if badIdent(def.Name) {
		add("BAD_NAME", "name", `invalid condition name: "`+def.Name+`"`)
	}

	// params: rule 1(이름) + rule 2(유일). paramType은 last-wins.
	paramType := make(map[string]string)
	seen := make(map[string]struct{})
	for i, p := range def.Params {
		if badIdent(p.Name) {
			add("BAD_NAME", "params["+strconv.Itoa(i)+"].name", `invalid param name: "`+p.Name+`"`)
		}
		if _, ok := seen[p.Name]; ok {
			add("DUP_PARAM", "params["+strconv.Itoa(i)+"].name", `duplicate param: "`+p.Name+`"`)
		}
		seen[p.Name] = struct{}{}
		paramType[p.Name] = p.Type
	}

	expectParam := func(param, path string, wanted []string) {
		t, ok := paramType[param]
		if !ok {
			add("UNKNOWN_PARAM", path+".param", `unknown param: "`+param+`"`)
			return
		}
		if !contains(wanted, t) {
			add("TYPE_MISMATCH", path+".param", `param "`+param+`" is `+t+`, expected `+strings.Join(wanted, "|"))
		}
	}

	visitLeaf := func(leaf *ConditionLeaf, path string) {
		switch leaf.Kind {
		case "time":
			expectParam(leaf.Param, path, []string{"timestamp"})
			rhs := leaf.Rhs
			if rhs.Kind == "literal" {
				if !rfc3339RE.MatchString(rhs.RFC3339) {
					add("BAD_TIMESTAMP", path+".rhs.rfc3339", `invalid RFC3339: "`+rhs.RFC3339+`"`)
				}
			} else if _, ok := paramType[rhs.Param]; !ok {
				add("UNKNOWN_PARAM", path+".rhs.param", `unknown param: "`+rhs.Param+`"`)
			} else if paramType[rhs.Param] != "timestamp" {
				add("TYPE_MISMATCH", path+".rhs.param", `param "`+rhs.Param+`" must be timestamp`)
			}
		case "ip":
			expectParam(leaf.Param, path, []string{"ipaddress"})
			if !isCidr(leaf.Cidr) {
				add("BAD_CIDR", path+".cidr", `invalid CIDR: "`+leaf.Cidr+`"`)
			}
		default: // "value"
			expectParam(leaf.Param, path, []string{"string", "int", "double", "bool"})
			t, ok := paramType[leaf.Param]
			if ok {
				if _, isVal := valueTypes[t]; isVal {
					v := derefValue(leaf.Value)
					okType := (t == "string" && v.Kind == ValueString) ||
						(t == "int" && v.Kind == ValueNumber && isSafeInteger(v.Num)) ||
						(t == "double" && v.Kind == ValueNumber && !math.IsNaN(v.Num) && !math.IsInf(v.Num, 0)) ||
						(t == "bool" && v.Kind == ValueBool)
					if !okType {
						add("TYPE_MISMATCH", path+".value", `value `+jsStringifyValue(v)+` does not match param type `+t)
					}
					if _, isOrder := orderOps[leaf.Op]; t == "bool" && isOrder {
						add("TYPE_MISMATCH", path+".op", `ordering op "`+leaf.Op+`" not allowed on bool param`)
					}
				}
			}
		}
	}

	var visit func(node ConditionNode, path string)
	visit = func(node ConditionNode, path string) {
		if node.isGroup() {
			if len(node.Group.Children) == 0 {
				add("EMPTY_GROUP", path, "group must have >= 1 child")
			}
			for i, c := range node.Group.Children {
				visit(c, path+".children["+strconv.Itoa(i)+"]")
			}
			return
		}
		visitLeaf(node.Leaf, path)
	}

	visit(def.Tree, "tree")
	return errs
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
