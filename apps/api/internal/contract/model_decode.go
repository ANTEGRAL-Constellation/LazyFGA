package contract

import (
	"encoding/json"
	"strconv"
)

// strict shape 디코딩 — zod 스키마 등가. 신뢰 못 할 JSON을 타입으로 파싱하기 전에 형태를
// 검사하고, 위반을 Issue{path, message}로 모두 수집한다(zod 내부 이슈 객체와 다름 —
// 승인된 편차 LFGA-22 §4.4-1). z.object는 non-strict라 알 수 없는 키는 무시(strip)한다.

type shapeDecoder struct{ issues []Issue }

func (d *shapeDecoder) add(path, msg string) {
	d.issues = append(d.issues, Issue{Path: path, Message: msg})
}

func fieldPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func indexPath(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

func (d *shapeDecoder) obj(v any, path string) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		d.add(path, "expected object")
		return nil, false
	}
	return m, true
}

// reqString: 필수 문자열. 부재/타입불일치 시 이슈.
func (d *shapeDecoder) reqString(m map[string]any, path, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		d.add(fieldPath(path, key), "required")
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		d.add(fieldPath(path, key), "expected string")
		return "", false
	}
	return s, true
}

func (d *shapeDecoder) requireStringField(m map[string]any, path, key string) {
	d.reqString(m, path, key)
}

// optString: 선택 문자열. 있으면 타입 검사.
func (d *shapeDecoder) optString(m map[string]any, path, key string) {
	if v, present := m[key]; present {
		if _, ok := v.(string); !ok {
			d.add(fieldPath(path, key), "expected string")
		}
	}
}

func (d *shapeDecoder) enumField(m map[string]any, path, key string, options ...string) {
	v, ok := m[key]
	if !ok {
		d.add(fieldPath(path, key), "required")
		return
	}
	s, ok := v.(string)
	if !ok || !contains(options, s) {
		d.add(fieldPath(path, key), "expected one of "+joinPipe(options))
	}
}

func (d *shapeDecoder) literalField(m map[string]any, path, key, lit string) {
	v, ok := m[key]
	if !ok {
		d.add(fieldPath(path, key), "required")
		return
	}
	if s, ok := v.(string); !ok || s != lit {
		d.add(fieldPath(path, key), `expected literal "`+lit+`"`)
	}
}

func (d *shapeDecoder) requireArray(m map[string]any, path, key string, each func(any, string)) {
	v, ok := m[key]
	if !ok {
		d.add(fieldPath(path, key), "required")
		return
	}
	d.validateArray(v, fieldPath(path, key), each)
}

func (d *shapeDecoder) validateArray(v any, path string, each func(any, string)) {
	arr, ok := v.([]any)
	if !ok {
		d.add(path, "expected array")
		return
	}
	for i, el := range arr {
		each(el, indexPath(path, i))
	}
}

func (d *shapeDecoder) validateStringElement(v any, path string) {
	if _, ok := v.(string); !ok {
		d.add(path, "expected string")
	}
}

func (d *shapeDecoder) validateSubjectRef(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	kind, ok := d.reqString(m, path, "kind")
	if !ok {
		return
	}
	switch kind {
	case "user":
		d.optString(m, path, "condition")
	case "group":
		d.requireStringField(m, path, "group")
		d.literalField(m, path, "relation", "member")
		d.optString(m, path, "condition")
	default:
		d.add(fieldPath(path, "kind"), `unknown discriminator "`+kind+`"`)
	}
}

func (d *shapeDecoder) validateParent(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	d.requireStringField(m, path, "relationName")
	d.requireArray(m, path, "parentTypes", d.validateStringElement)
}

func (d *shapeDecoder) validateRole(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	d.requireStringField(m, path, "name")
	d.requireArray(m, path, "assignableBy", d.validateSubjectRef)
}

func (d *shapeDecoder) validatePermission(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	d.requireStringField(m, path, "name")
	d.requireArray(m, path, "grantedByRoles", d.validateStringElement)
	d.requireArray(m, path, "inheritFromParents", d.validateStringElement)
}

func (d *shapeDecoder) validateGroup(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	d.requireStringField(m, path, "name")
	d.requireArray(m, path, "memberTypes", d.validateSubjectRef)
}

func (d *shapeDecoder) validateResource(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	d.requireStringField(m, path, "name")
	d.requireArray(m, path, "parents", d.validateParent)
	d.requireArray(m, path, "roles", d.validateRole)
	d.requireArray(m, path, "permissions", d.validatePermission)
}

var paramTypeSet = map[string]struct{}{
	"timestamp": {}, "ipaddress": {}, "string": {}, "int": {}, "double": {}, "bool": {},
}

func (d *shapeDecoder) validateParam(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	d.requireStringField(m, path, "name")
	t, ok := m["type"]
	if !ok {
		d.add(fieldPath(path, "type"), "required")
		return
	}
	if s, ok := t.(string); !ok {
		d.add(fieldPath(path, "type"), "expected string")
	} else if _, isType := paramTypeSet[s]; !isType {
		d.add(fieldPath(path, "type"), "invalid param type")
	}
}

func (d *shapeDecoder) validateTimeRhs(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	kind, ok := d.reqString(m, path, "kind")
	if !ok {
		return
	}
	switch kind {
	case "literal":
		d.requireStringField(m, path, "rfc3339")
	case "param":
		d.requireStringField(m, path, "param")
	default:
		d.add(fieldPath(path, "kind"), `unknown discriminator "`+kind+`"`)
	}
}

func (d *shapeDecoder) validateLeaf(m map[string]any, path string) {
	kind, ok := d.reqString(m, path, "kind")
	if !ok {
		return
	}
	switch kind {
	case "time":
		d.requireStringField(m, path, "param")
		d.enumField(m, path, "op", "lt", "lte", "gt", "gte")
		rhs, present := m["rhs"]
		if !present {
			d.add(fieldPath(path, "rhs"), "required")
		} else {
			d.validateTimeRhs(rhs, fieldPath(path, "rhs"))
		}
	case "ip":
		d.requireStringField(m, path, "param")
		d.literalField(m, path, "op", "in_cidr")
		d.requireStringField(m, path, "cidr")
	case "value":
		d.requireStringField(m, path, "param")
		d.enumField(m, path, "op", "eq", "neq", "lt", "lte", "gt", "gte")
		val, present := m["value"]
		if !present {
			d.add(fieldPath(path, "value"), "required")
		} else {
			switch val.(type) {
			case string, float64, bool:
			default:
				d.add(fieldPath(path, "value"), "expected string|number|boolean")
			}
		}
	default:
		d.add(fieldPath(path, "kind"), `unknown discriminator "`+kind+`"`)
	}
}

func (d *shapeDecoder) validateNode(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	// zod 순서 union 재현: group 브랜치(op∈and|or + children 배열)가 성공하면 group,
	// 실패하면 leaf 브랜치로 폴백한다. children 키 존재만으로 group을 강제하지 않는다 —
	// zod object는 잉여 키를 무시하므로 잉여 children을 가진 유효 leaf도 수용해야 한다.
	g := &shapeDecoder{}
	g.enumField(m, path, "op", "and", "or")
	g.requireArray(m, path, "children", g.validateNode)
	if len(g.issues) == 0 {
		return
	}
	l := &shapeDecoder{}
	l.validateLeaf(m, path)
	if len(l.issues) == 0 {
		return
	}
	// 둘 다 실패: 더 그럴듯한 브랜치의 이슈를 보고한다(children 보유 + kind 부재 = group 의도).
	if _, hasChildren := m["children"]; hasChildren {
		if _, hasKind := m["kind"]; !hasKind {
			d.issues = append(d.issues, g.issues...)
			return
		}
	}
	d.issues = append(d.issues, l.issues...)
}

func (d *shapeDecoder) validateCondition(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	d.requireStringField(m, path, "name")
	d.requireArray(m, path, "params", d.validateParam)
	tree, present := m["tree"]
	if !present {
		d.add(fieldPath(path, "tree"), "required")
	} else {
		d.validateNode(tree, fieldPath(path, "tree"))
	}
}

func (d *shapeDecoder) validateModel(v any, path string) {
	m, ok := d.obj(v, path)
	if !ok {
		return
	}
	sv, present := m["schemaVersion"]
	if !present {
		d.add(fieldPath(path, "schemaVersion"), "required")
	} else if s, ok := sv.(string); !ok || s != "1.1" {
		d.add(fieldPath(path, "schemaVersion"), `expected literal "1.1"`)
	}
	d.requireArray(m, path, "groups", d.validateGroup)
	d.requireArray(m, path, "resources", d.validateResource)
	if c, present := m["conditions"]; present {
		d.validateArray(c, fieldPath(path, "conditions"), d.validateCondition)
	}
}

// DecodeModelIR는 신뢰 못 할 JSON을 ModelIR로 strict 디코딩한다. 형태 위반 시 field-level Issue를
// 반환한다(nil ModelIR). 유효하면 타입 디코딩 결과를 돌려준다.
func DecodeModelIR(data []byte) (*ModelIR, []Issue) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, []Issue{{Path: "", Message: "invalid JSON: " + err.Error()}}
	}
	d := &shapeDecoder{}
	d.validateModel(raw, "")
	if len(d.issues) > 0 {
		return nil, d.issues
	}
	var ir ModelIR
	if err := json.Unmarshal(data, &ir); err != nil {
		return nil, []Issue{{Path: "", Message: "decode failed: " + err.Error()}}
	}
	return &ir, nil
}

func joinPipe(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "|"
		}
		out += x
	}
	return out
}
