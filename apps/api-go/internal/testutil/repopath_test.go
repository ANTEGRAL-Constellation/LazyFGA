package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepoPathAnchorsAtRoot는 RepoPath가 저장소 루트를 정확히 가리키는지(대표 파일 존재로)
// 확인한다.
func TestRepoPathAnchorsAtRoot(t *testing.T) {
	// 루트에는 CLAUDE.md 와 package.json 이 있다.
	for _, marker := range []string{"CLAUDE.md", "package.json", "CONCEPT.md"} {
		p := RepoPath(marker)
		if !filepath.IsAbs(p) {
			t.Fatalf("RepoPath(%q) = %q, want absolute", marker, p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("RepoPath(%q) = %q not found: %v", marker, p, err)
		}
	}
}

// TestRepoPathJoinsParts는 다중 파트 결합을 확인한다.
func TestRepoPathJoinsParts(t *testing.T) {
	p := RepoPath("packages", "shared", "src")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("RepoPath(packages/shared/src) = %q not found: %v", p, err)
	}
	if filepath.Base(p) != "src" {
		t.Fatalf("base = %q, want src", filepath.Base(p))
	}
}

// TestRepoPathNoParts는 인자 없이 루트 자체를 반환하는지 확인한다.
func TestRepoPathNoParts(t *testing.T) {
	root := RepoPath()
	if _, err := os.Stat(filepath.Join(root, "pnpm-workspace.yaml")); err != nil {
		t.Fatalf("root %q missing pnpm-workspace.yaml: %v", root, err)
	}
}
