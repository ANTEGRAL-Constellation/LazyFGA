package democli

import (
	"encoding/json"
	"testing"
)

func decodeIR(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestAddCondition(t *testing.T) {
	def := map[string]any{"name": "c1", "tree": map[string]any{"kind": "true"}}

	t.Run("appends when absent", func(t *testing.T) {
		ir := decodeIR(t, `{"resources":[]}`)
		out := addCondition(ir, def)
		conds, _ := asSlice(out["conditions"])
		if len(conds) != 1 {
			t.Fatalf("conditions len = %d, want 1", len(conds))
		}
		// 입력 불변(순수).
		if _, present := ir["conditions"]; present {
			t.Error("addCondition mutated its input")
		}
	})

	t.Run("appends to existing", func(t *testing.T) {
		ir := decodeIR(t, `{"conditions":[{"name":"other"}]}`)
		out := addCondition(ir, def)
		conds, _ := asSlice(out["conditions"])
		if len(conds) != 2 {
			t.Fatalf("conditions len = %d, want 2", len(conds))
		}
	})

	t.Run("no-op on duplicate name", func(t *testing.T) {
		ir := decodeIR(t, `{"conditions":[{"name":"c1"}]}`)
		out := addCondition(ir, def)
		conds, _ := asSlice(out["conditions"])
		if len(conds) != 1 {
			t.Fatalf("duplicate should be no-op; len = %d, want 1", len(conds))
		}
	})
}

func TestSetAssignmentCondition(t *testing.T) {
	base := `{"resources":[{"name":"document","roles":[{"name":"owner","assignableBy":[{"kind":"user"},{"kind":"group"}]}]}]}`

	t.Run("sets condition on target index", func(t *testing.T) {
		ir := decodeIR(t, base)
		cond := "non_expired"
		out := setAssignmentCondition(ir, "document", "owner", 0, &cond)
		ref := roleRef(t, out, "document", "owner", 0)
		if ref["condition"] != "non_expired" {
			t.Fatalf("condition = %v, want non_expired", ref["condition"])
		}
		// 입력 불변.
		inRef := roleRef(t, ir, "document", "owner", 0)
		if _, present := inRef["condition"]; present {
			t.Error("setAssignmentCondition mutated its input")
		}
	})

	t.Run("nil clears condition", func(t *testing.T) {
		ir := decodeIR(t, `{"resources":[{"name":"document","roles":[{"name":"owner","assignableBy":[{"kind":"user","condition":"x"}]}]}]}`)
		out := setAssignmentCondition(ir, "document", "owner", 0, nil)
		ref := roleRef(t, out, "document", "owner", 0)
		if _, present := ref["condition"]; present {
			t.Fatalf("condition should be cleared, got %v", ref["condition"])
		}
	})

	t.Run("no-op unknown type", func(t *testing.T) {
		ir := decodeIR(t, base)
		cond := "x"
		out := setAssignmentCondition(ir, "nope", "owner", 0, &cond)
		if roleRef(t, out, "document", "owner", 0)["condition"] != nil {
			t.Error("unknown type should be no-op")
		}
	})

	t.Run("no-op unknown role", func(t *testing.T) {
		ir := decodeIR(t, base)
		cond := "x"
		out := setAssignmentCondition(ir, "document", "nope", 0, &cond)
		if roleRef(t, out, "document", "owner", 0)["condition"] != nil {
			t.Error("unknown role should be no-op")
		}
	})

	t.Run("no-op out of range index", func(t *testing.T) {
		ir := decodeIR(t, base)
		cond := "x"
		for _, idx := range []int{-1, 5} {
			out := setAssignmentCondition(ir, "document", "owner", idx, &cond)
			if roleRef(t, out, "document", "owner", 0)["condition"] != nil {
				t.Errorf("index %d should be no-op", idx)
			}
		}
	})
}

func TestSetAssignmentCondition_toleratesNonObjectElements(t *testing.T) {
	// resources/roles/assignableBy에 비-객체 원소가 섞여 있어도 대상은 정상 처리한다
	// (findRole/asMap의 방어적 continue 경로 커버).
	ir := decodeIR(t, `{"resources":["skip",{"name":"document","roles":["skip",{"name":"owner","assignableBy":[{"kind":"user"},"skip"]}]}]}`)
	cond := "c"
	out := setAssignmentCondition(ir, "document", "owner", 0, &cond)
	if roleRef(t, out, "document", "owner", 0)["condition"] != "c" {
		t.Error("condition should be set on the first (object) assignableBy entry")
	}

	// 대상 인덱스가 비-객체면 no-op(asMap !ok 경로).
	out2 := setAssignmentCondition(ir, "document", "owner", 1, &cond)
	ab, _ := asSlice(findRole(out2, "document", "owner")["assignableBy"])
	if ab[1] != "skip" {
		t.Errorf("non-object index should be left untouched, got %v", ab[1])
	}
}

// roleRef는 resources[type].roles[role].assignableBy[idx] map을 꺼낸다(테스트 헬퍼).
func roleRef(t *testing.T, ir map[string]any, typeName, role string, idx int) map[string]any {
	t.Helper()
	rl := findRole(ir, typeName, role)
	if rl == nil {
		t.Fatalf("role %s/%s not found", typeName, role)
	}
	ab, _ := asSlice(rl["assignableBy"])
	m, _ := asMap(ab[idx])
	return m
}
