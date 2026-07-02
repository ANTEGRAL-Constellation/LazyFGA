package idp

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func strp(s string) *string { return &s }

func baseEvent() *IdpEvent {
	return &IdpEvent{
		Type:       "user.grant.added",
		Subject:    Subject{Type: "user", ID: "alice"},
		Attributes: map[string]any{"project": "123", "role": "editor"},
	}
}

func writeRule(over func(*MappingRule)) MappingRule {
	r := MappingRule{
		EventType:     "user.grant.added",
		Match:         nil,
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"},
		Op:            "write",
		Priority:      0,
	}
	if over != nil {
		over(&r)
	}
	return r
}

func TestMatchRule(t *testing.T) {
	ev := baseEvent()

	t.Run("matches on eventType + predicates", func(t *testing.T) {
		if !matchRule(writeRule(func(r *MappingRule) { r.Match = []MatchPredicate{{Field: "attributes.role", Equals: "editor"}} }), ev) {
			t.Error("expected match")
		}
		if matchRule(writeRule(func(r *MappingRule) { r.Match = []MatchPredicate{{Field: "attributes.role", Equals: "viewer"}} }), ev) {
			t.Error("predicate mismatch must not match")
		}
		if matchRule(writeRule(func(r *MappingRule) { r.EventType = "other" }), ev) {
			t.Error("eventType mismatch must not match")
		}
	})

	t.Run("subject placeholder field matches", func(t *testing.T) {
		if !matchRule(writeRule(func(r *MappingRule) { r.Match = []MatchPredicate{{Field: "subject", Equals: "alice"}} }), ev) {
			t.Error("subject predicate must match")
		}
	})

	t.Run("fan-out rule only matches when the array attribute is non-empty", func(t *testing.T) {
		withRoles := &IdpEvent{Type: ev.Type, Subject: ev.Subject, Attributes: map[string]any{"project": "123", "roleKeys": []string{"a", "b"}}}
		empty := &IdpEvent{Type: ev.Type, Subject: ev.Subject, Attributes: map[string]any{"project": "123", "roleKeys": []string{}}}
		fr := writeRule(func(r *MappingRule) {
			r.FanOut = strp("roleKeys")
			r.TupleTemplate = TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "team:{{attributes.project}}"}
		})
		if !matchRule(fr, withRoles) {
			t.Error("non-empty array must match")
		}
		if matchRule(fr, empty) {
			t.Error("empty array must not match")
		}
	})
}

func TestRenderTuple(t *testing.T) {
	t.Run("substitutes placeholders (subject + subject.id alias)", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": "123"}}
		tuple, err := renderTuple(writeRule(nil).TupleTemplate, e2, nil)
		if err != "" || !reflect.DeepEqual(tuple, &RenderedTuple{User: "user:alice", Relation: "member", Object: "team:123"}) {
			t.Fatalf("got %+v err=%q", tuple, err)
		}
		tuple2, err2 := renderTuple(TupleTemplate{User: "user:{{subject.id}}", Relation: "member", Object: "team:{{attributes.project}}"}, e2, nil)
		if err2 != "" || tuple2.User != "user:alice" {
			t.Fatalf("subject.id alias: %+v err=%q", tuple2, err2)
		}
	})

	t.Run("rejects forbidden chars in substituted value", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": "1#member"}}
		if _, err := renderTuple(writeRule(nil).TupleTemplate, e2, nil); err == "" {
			t.Error("forbidden char must error")
		}
	})

	t.Run("rejects unresolved placeholder", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{}}
		if _, err := renderTuple(writeRule(nil).TupleTemplate, e2, nil); err == "" {
			t.Error("unresolved placeholder must error")
		}
	})

	t.Run("array attribute in a scalar slot errors", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": []string{"a", "b"}}}
		if _, err := renderTuple(writeRule(nil).TupleTemplate, e2, nil); err == "" {
			t.Error("array in scalar slot must error")
		}
	})

	t.Run("{{item}} without a provided item errors", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": "1"}}
		if _, err := renderTuple(TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "team:{{attributes.project}}"}, e2, nil); err != "{{item}} used without fan-out" {
			t.Errorf("err = %q", err)
		}
	})

	t.Run("{{item}} binds the fan-out element", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": "1"}}
		tuple, err := renderTuple(TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "team:{{attributes.project}}"}, e2, strp("editor"))
		if err != "" || !reflect.DeepEqual(tuple, &RenderedTuple{User: "user:alice", Relation: "editor", Object: "team:1"}) {
			t.Fatalf("got %+v err=%q", tuple, err)
		}
	})

	t.Run("requires literal type: prefix", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"t": "document", "project": "123"}}
		if _, err := renderTuple(TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "{{attributes.project}}"}, e2, nil); err != "object template must start with a literal type: prefix" {
			t.Errorf("err = %q", err)
		}
		if _, err := renderTuple(TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "{{attributes.t}}:{{attributes.project}}"}, e2, nil); err != "object template must start with a literal type: prefix" {
			t.Errorf("err = %q", err)
		}
	})

	t.Run("invalid relation after render", func(t *testing.T) {
		e2 := &IdpEvent{Type: "x", Subject: Subject{Type: "user", ID: "a"}, Attributes: map[string]any{}}
		if _, err := renderTuple(TupleTemplate{User: "user:a", Relation: "{{item}}", Object: "team:1"}, e2, strp("")); err != `invalid relation ""` {
			t.Errorf("empty item → invalid relation, got %q", err)
		}
	})
}

type fakeApply struct {
	audits []string
	write  func(op string, tuple RenderedTuple) (string, error)
}

func (f *fakeApply) deps() ApplyDeps {
	return ApplyDeps{
		WriteTuple: f.write,
		Audit:      func(action string, _ map[string]any) { f.audits = append(f.audits, action) },
	}
}

func TestApplyEvents(t *testing.T) {
	ev := baseEvent()

	t.Run("applies matched rules; counts applied", func(t *testing.T) {
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) { return "applied", nil }}
		res, err := ApplyEvents([]IdpEvent{*ev}, []MappingRule{writeRule(nil)}, f.deps())
		if err != nil || res != (ApplyResult{Applied: 1}) {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})

	t.Run("no matching rule → skipped", func(t *testing.T) {
		e2 := &IdpEvent{Type: "unmatched", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{}}
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) { return "applied", nil }}
		res, _ := ApplyEvents([]IdpEvent{*e2}, []MappingRule{writeRule(nil)}, f.deps())
		if res != (ApplyResult{Skipped: 1}) {
			t.Fatalf("res=%+v", res)
		}
	})

	t.Run("idempotent write → skipped, not failed", func(t *testing.T) {
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) { return "skipped", nil }}
		res, _ := ApplyEvents([]IdpEvent{*ev}, []MappingRule{writeRule(nil)}, f.deps())
		if res != (ApplyResult{Skipped: 1}) {
			t.Fatalf("res=%+v", res)
		}
	})

	t.Run("render error → failed, continues", func(t *testing.T) {
		e2 := &IdpEvent{Type: "user.grant.added", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{}}
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) { return "applied", nil }}
		res, _ := ApplyEvents([]IdpEvent{*e2}, []MappingRule{writeRule(nil)}, f.deps())
		if res != (ApplyResult{Failed: 1}) {
			t.Fatalf("res=%+v", res)
		}
	})

	t.Run("deterministic write error → failed, continues", func(t *testing.T) {
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) {
			return "", &WriteError{Transient: false, Msg: "type_not_found"}
		}}
		res, err := ApplyEvents([]IdpEvent{*ev}, []MappingRule{writeRule(nil)}, f.deps())
		if err != nil || res != (ApplyResult{Failed: 1}) {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})

	t.Run("non-WriteError write error → failed, continues", func(t *testing.T) {
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) { return "", errors.New("boom") }}
		res, err := ApplyEvents([]IdpEvent{*ev}, []MappingRule{writeRule(nil)}, f.deps())
		if err != nil || res != (ApplyResult{Failed: 1}) {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})

	t.Run("transient write error → rethrows (→ 502)", func(t *testing.T) {
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) {
			return "", &WriteError{Transient: true, Msg: "fetch failed"}
		}}
		_, err := ApplyEvents([]IdpEvent{*ev}, []MappingRule{writeRule(nil)}, f.deps())
		var we *WriteError
		if !errors.As(err, &we) || !we.Transient {
			t.Fatalf("expected transient WriteError, got %v", err)
		}
	})

	t.Run("rules applied in priority order", func(t *testing.T) {
		var order []string
		f := &fakeApply{write: func(_ string, tup RenderedTuple) (string, error) {
			order = append(order, tup.Relation)
			return "applied", nil
		}}
		r1 := writeRule(func(r *MappingRule) {
			r.Priority = 2
			r.TupleTemplate = TupleTemplate{User: "user:a", Relation: "r2", Object: "team:1"}
		})
		r2 := writeRule(func(r *MappingRule) {
			r.Priority = 1
			r.TupleTemplate = TupleTemplate{User: "user:a", Relation: "r1", Object: "team:1"}
		})
		_, _ = ApplyEvents([]IdpEvent{*ev}, []MappingRule{r1, r2}, f.deps())
		if !reflect.DeepEqual(order, []string{"r1", "r2"}) {
			t.Fatalf("order = %v", order)
		}
	})
}

func TestApplyEvents_FanOut(t *testing.T) {
	fanRule := writeRule(func(r *MappingRule) {
		r.FanOut = strp("roleKeys")
		r.TupleTemplate = TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "team:{{attributes.project}}"}
	})

	t.Run("emits one tuple per array element", func(t *testing.T) {
		e2 := &IdpEvent{Type: "user.grant.added", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": "1", "roleKeys": []string{"editor", "viewer"}}}
		var rels []string
		f := &fakeApply{write: func(_ string, tup RenderedTuple) (string, error) {
			rels = append(rels, tup.Relation)
			return "applied", nil
		}}
		res, _ := ApplyEvents([]IdpEvent{*e2}, []MappingRule{fanRule}, f.deps())
		if res != (ApplyResult{Applied: 2}) {
			t.Fatalf("res=%+v", res)
		}
		sort.Strings(rels)
		if !reflect.DeepEqual(rels, []string{"editor", "viewer"}) {
			t.Fatalf("rels=%v", rels)
		}
	})

	t.Run("a forbidden-char element fails only that element", func(t *testing.T) {
		e2 := &IdpEvent{Type: "user.grant.added", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": "1", "roleKeys": []string{"editor", "bad role"}}}
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) { return "applied", nil }}
		res, _ := ApplyEvents([]IdpEvent{*e2}, []MappingRule{fanRule}, f.deps())
		if res != (ApplyResult{Applied: 1, Failed: 1}) {
			t.Fatalf("res=%+v", res)
		}
	})

	t.Run("empty array → rule does not match → skipped", func(t *testing.T) {
		e2 := &IdpEvent{Type: "user.grant.added", Subject: Subject{Type: "user", ID: "alice"}, Attributes: map[string]any{"project": "1", "roleKeys": []string{}}}
		f := &fakeApply{write: func(string, RenderedTuple) (string, error) { return "applied", nil }}
		res, _ := ApplyEvents([]IdpEvent{*e2}, []MappingRule{fanRule}, f.deps())
		if res != (ApplyResult{Skipped: 1}) {
			t.Fatalf("res=%+v", res)
		}
	})
}
