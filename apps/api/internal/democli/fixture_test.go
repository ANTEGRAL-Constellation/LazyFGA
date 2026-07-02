package democli

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/contract"
	"github.com/antegral-constellation/lazyfga/api/internal/testutil"
)

// TestEmbeddedFixture_matchesSource는 임베드 사본이 packages/shared의 원본 fixture와 바이트 동등한지
// 검증한다(LFGA-27 §4.3 parity checksum). 원본이 바뀌면 이 테스트가 사본 갱신을 강제한다.
func TestEmbeddedFixture_matchesSource(t *testing.T) {
	src := testutil.RepoPath("packages", "shared", "src", "__fixtures__", "doc-folder-team.ir.json")
	want, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read source fixture: %v", err)
	}
	if !bytes.Equal(want, docFolderTeamIR) {
		t.Fatalf("embedded doc-folder-team.ir.json differs from source %s\n(copy the source into internal/democli/ to resync)", src)
	}
}

// TestDemoIR_isValidModel은 데모 IR(편집 적용본)이 실제 발행 디코더를 통과하는지 확인한다 —
// Run이 POST /model에서 422를 맞지 않음을 보장한다.
func TestDemoIR_isValidModel(t *testing.T) {
	ir, err := demoIR()
	if err != nil {
		t.Fatalf("demoIR: %v", err)
	}
	raw, err := json.Marshal(ir)
	if err != nil {
		t.Fatalf("marshal demoIR: %v", err)
	}
	_, issues := contract.DecodeModelIR(raw)
	if len(issues) > 0 {
		t.Fatalf("demo IR failed shape validation: %+v", issues)
	}
}
