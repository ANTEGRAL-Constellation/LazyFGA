package db

import (
	"context"
	"testing"
	"time"
)

func TestConnect_invalidURL(t *testing.T) {
	if _, err := Connect(context.Background(), "postgres://user@host:notaport/db"); err == nil {
		t.Fatal("expected error for invalid database URL")
	}
}

func TestConnect_validURLIsLazy(t *testing.T) {
	// 지연 연결이므로 실제 DB가 없어도 풀 생성은 성공한다.
	pool, err := Connect(context.Background(), "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err != nil {
		t.Fatalf("Connect should be lazy: %v", err)
	}
	defer pool.Close()
}

func TestPing_nilPool(t *testing.T) {
	if Ping(context.Background(), nil) {
		t.Fatal("Ping(nil) should be false")
	}
}

func TestPing_unreachable(t *testing.T) {
	pool, err := Connect(context.Background(), "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if Ping(ctx, pool) {
		t.Fatal("Ping to unreachable server should be false")
	}
}
