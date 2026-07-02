package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

// explainability 공유 타입(LFGA-11). 허용=witnessing path, 거부=best-effort missingLinks.
// ReasonStep.direct(bool)와 .on은 값이 false여도 항상 직렬화한다(web이 읽음). 나머지 optional
// 필드(path/missingLinks/truncated, group/groupObject/parentObject)는 없으면 생략한다.

// ReasonStep: via="role"(role/on/direct[/group/groupObject]) | via="parent"(relation/parent[/parentObject]).
type ReasonStep struct {
	Via          string
	Role         string  // role
	On           string  // role
	Direct       bool    // role — 항상 직렬화.
	Group        *string // role optional
	GroupObject  *string // role optional
	Relation     string  // parent
	Parent       string  // parent
	ParentObject *string // parent optional
}

func (s ReasonStep) MarshalJSON() ([]byte, error) {
	switch s.Via {
	case "role":
		return marshalNoEscape(struct {
			Via         string  `json:"via"`
			Role        string  `json:"role"`
			On          string  `json:"on"`
			Direct      bool    `json:"direct"`
			Group       *string `json:"group,omitempty"`
			GroupObject *string `json:"groupObject,omitempty"`
		}{s.Via, s.Role, s.On, s.Direct, s.Group, s.GroupObject})
	case "parent":
		return marshalNoEscape(struct {
			Via          string  `json:"via"`
			Relation     string  `json:"relation"`
			Parent       string  `json:"parent"`
			ParentObject *string `json:"parentObject,omitempty"`
		}{s.Via, s.Relation, s.Parent, s.ParentObject})
	default:
		return nil, errors.New("contract: invalid ReasonStep via")
	}
}

func (s *ReasonStep) UnmarshalJSON(b []byte) error {
	var probe struct {
		Via          string  `json:"via"`
		Role         string  `json:"role"`
		On           string  `json:"on"`
		Direct       bool    `json:"direct"`
		Group        *string `json:"group"`
		GroupObject  *string `json:"groupObject"`
		Relation     string  `json:"relation"`
		Parent       string  `json:"parent"`
		ParentObject *string `json:"parentObject"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	switch probe.Via {
	case "role":
		*s = ReasonStep{Via: "role", Role: probe.Role, On: probe.On, Direct: probe.Direct, Group: probe.Group, GroupObject: probe.GroupObject}
	case "parent":
		*s = ReasonStep{Via: "parent", Relation: probe.Relation, Parent: probe.Parent, ParentObject: probe.ParentObject}
	default:
		return errors.New("contract: unknown ReasonStep via: " + probe.Via)
	}
	return nil
}

// MissingLink: kind="role"(anyOf/on) | kind="parent"(relation/needs).
type MissingLink struct {
	Kind     string
	AnyOf    []string // role
	On       string   // role
	Relation string   // parent
	Needs    string   // parent
}

func (m MissingLink) MarshalJSON() ([]byte, error) {
	switch m.Kind {
	case "role":
		return marshalNoEscape(struct {
			Kind  string   `json:"kind"`
			AnyOf []string `json:"anyOf"`
			On    string   `json:"on"`
		}{m.Kind, m.AnyOf, m.On})
	case "parent":
		return marshalNoEscape(struct {
			Kind     string `json:"kind"`
			Relation string `json:"relation"`
			Needs    string `json:"needs"`
		}{m.Kind, m.Relation, m.Needs})
	default:
		return nil, errors.New("contract: invalid MissingLink kind")
	}
}

func (m *MissingLink) UnmarshalJSON(b []byte) error {
	var probe struct {
		Kind     string   `json:"kind"`
		AnyOf    []string `json:"anyOf"`
		On       string   `json:"on"`
		Relation string   `json:"relation"`
		Needs    string   `json:"needs"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	switch probe.Kind {
	case "role":
		*m = MissingLink{Kind: "role", AnyOf: probe.AnyOf, On: probe.On}
	case "parent":
		*m = MissingLink{Kind: "parent", Relation: probe.Relation, Needs: probe.Needs}
	default:
		return errors.New("contract: unknown MissingLink kind: " + probe.Kind)
	}
	return nil
}

// ReasonResult: decision + (allow) path | (deny) missingLinks + text[+ truncated].
// path/missingLinks는 nil(부재)과 빈 배열(존재)을 구분한다 — TS explain()은 deny에서
// denyLinks가 빈 배열이어도 `"missingLinks":[]`를 그대로 직렬화한다(reason.ts). 필드 순서도
// TS 객체 리터럴 순서(decision → path|missingLinks → truncated → text)를 바이트 단위로 따른다.
type ReasonResult struct {
	Decision     bool
	Path         []ReasonStep  // nil=부재, non-nil=존재(빈 배열 포함)
	MissingLinks []MissingLink // nil=부재, non-nil=존재(빈 배열 포함)
	Text         string
	Truncated    *bool
}

func (r ReasonResult) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"decision":`)
	b.WriteString(strconv.FormatBool(r.Decision))
	if r.Path != nil {
		pb, err := marshalNoEscape(r.Path)
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"path":`)
		b.Write(pb)
	}
	if r.MissingLinks != nil {
		mb, err := marshalNoEscape(r.MissingLinks)
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"missingLinks":`)
		b.Write(mb)
	}
	if r.Truncated != nil {
		b.WriteString(`,"truncated":`)
		b.WriteString(strconv.FormatBool(*r.Truncated))
	}
	b.WriteString(`,"text":`)
	b.WriteString(jsutil.JSONString(r.Text))
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (r *ReasonResult) UnmarshalJSON(data []byte) error {
	var probe struct {
		Decision     bool          `json:"decision"`
		Path         []ReasonStep  `json:"path"`
		MissingLinks []MissingLink `json:"missingLinks"`
		Text         string        `json:"text"`
		Truncated    *bool         `json:"truncated"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	*r = ReasonResult{
		Decision:     probe.Decision,
		Path:         probe.Path,
		MissingLinks: probe.MissingLinks,
		Text:         probe.Text,
		Truncated:    probe.Truncated,
	}
	return nil
}
