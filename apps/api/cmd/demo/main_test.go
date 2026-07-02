package main

import (
	"context"
	"io"
	"testing"
)

func TestRun_usage(t *testing.T) {
	if code := run(context.Background(), []string{"demo"}, io.Discard, io.Discard); code != 2 {
		t.Fatalf("no subcommand → exit %d, want 2", code)
	}
}

func TestRun_unknownSubcommand(t *testing.T) {
	if code := run(context.Background(), []string{"demo", "frobnicate"}, io.Discard, io.Discard); code != 2 {
		t.Fatalf("unknown subcommand → exit %d, want 2", code)
	}
}

func TestRun_badDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "://not-a-dsn")
	if code := run(context.Background(), []string{"demo", "run"}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("bad DSN → exit %d, want 1", code)
	}
}

// TestRun_dispatch는 run/reset 디스패치 경로를 탄다(API 미도달 → democli가 오류 → exit 1).
func TestRun_dispatch(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db")
	t.Setenv("API_BASE", "http://127.0.0.1:1")
	for _, sub := range []string{"run", "reset"} {
		if code := run(context.Background(), []string{"demo", sub}, io.Discard, io.Discard); code != 1 {
			t.Fatalf("%s with unreachable API → exit %d, want 1", sub, code)
		}
	}
}
