package main

import (
	"context"
	"io"
	"testing"
)

func TestRun_badDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "://not-a-dsn")
	if code := run(context.Background(), io.Discard, io.Discard); code != 1 {
		t.Fatalf("bad DSN → exit %d, want 1", code)
	}
}

// TestRun_dbUnreachable는 pgx 풀 조립 성공 후 저장소 접근 실패(연결 거부) → exit 1 경로를 탄다.
func TestRun_dbUnreachable(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db")
	if code := run(context.Background(), io.Discard, io.Discard); code != 1 {
		t.Fatalf("unreachable DB → exit %d, want 1", code)
	}
}
