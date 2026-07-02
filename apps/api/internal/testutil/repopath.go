// Package testutil은 테스트 지원 유틸을 제공한다. RepoPath는 cross-language parity 코퍼스
// 처럼 저장소 루트 기준 경로를 실행 위치(cwd)와 무관하게 안정적으로 해석한다.
package testutil

import (
	"path/filepath"
	"runtime"
)

// RepoPath는 저장소 루트(.../lazyfga) 아래 parts를 이어붙인 절대경로를 반환한다.
// runtime.Caller로 이 소스 파일 위치를 기준삼아 루트를 계산하므로 cwd에 의존하지 않고,
// apps/api-go → apps/api 이동 후에도 깊이가 같아 그대로 동작한다
// (파일: .../lazyfga/apps/<api|api-go>/internal/testutil/repopath.go → 네 단계 상위가 루트).
func RepoPath(parts ...string) string {
	// runtime.Caller(0)은 이 소스 파일 경로를 항상 돌려준다(직접 호출이라 실패 불가).
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	return filepath.Join(append([]string{root}, parts...)...)
}
