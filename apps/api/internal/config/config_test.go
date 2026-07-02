package config

import (
	"os"
	"testing"
)

// unset은 t.Setenv로 원복을 등록한 뒤 실제로는 변수를 제거한다(진짜 "미설정" 상태).
func unset(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestLoad_defaults(t *testing.T) {
	unset(t, "PORT", "DATABASE_URL", "OPENFGA_API_URL", "LAZYFGA_STORE_ID", "ADMIN_TOKEN")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Port != defaultPort {
		t.Errorf("Port = %d, want %d", got.Port, defaultPort)
	}
	if got.DatabaseURL != defaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, want %q", got.DatabaseURL, defaultDatabaseURL)
	}
	if got.OpenFGAAPIURL != defaultOpenFGAAPIURL {
		t.Errorf("OpenFGAAPIURL = %q, want %q", got.OpenFGAAPIURL, defaultOpenFGAAPIURL)
	}
	if got.StoreID != "" {
		t.Errorf("StoreID = %q, want empty", got.StoreID)
	}
	if got.AdminToken != "" {
		t.Errorf("AdminToken = %q, want empty", got.AdminToken)
	}
}

func TestLoad_emptyStringPreserved(t *testing.T) {
	// TS `env ?? default`는 명시적 빈 문자열을 보존한다(기본값으로 조용히 대체 금지).
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("OPENFGA_API_URL", "")
	t.Setenv("LAZYFGA_STORE_ID", "")
	t.Setenv("ADMIN_TOKEN", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty preserved", got.DatabaseURL)
	}
	if got.OpenFGAAPIURL != "" {
		t.Errorf("OpenFGAAPIURL = %q, want empty preserved", got.OpenFGAAPIURL)
	}
	// PORT 빈 문자열만은 기본 포트로 폴백(승인 편차 LFGA-22 §4.4-4).
	if got.Port != defaultPort {
		t.Errorf("Port = %d, want %d", got.Port, defaultPort)
	}
	if got.StoreID != "" {
		t.Errorf("StoreID = %q, want empty", got.StoreID)
	}
}

func TestLoad_explicitValues(t *testing.T) {
	t.Setenv("PORT", "9099")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/x")
	t.Setenv("OPENFGA_API_URL", "http://fga:8088")
	t.Setenv("LAZYFGA_STORE_ID", "store-123")
	t.Setenv("ADMIN_TOKEN", "s3cr3t")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Port != 9099 {
		t.Errorf("Port = %d, want 9099", got.Port)
	}
	if got.DatabaseURL != "postgres://u:p@db:5432/x" {
		t.Errorf("DatabaseURL = %q", got.DatabaseURL)
	}
	if got.OpenFGAAPIURL != "http://fga:8088" {
		t.Errorf("OpenFGAAPIURL = %q", got.OpenFGAAPIURL)
	}
	if got.StoreID != "store-123" {
		t.Errorf("StoreID = %q", got.StoreID)
	}
	if got.AdminToken != "s3cr3t" {
		t.Errorf("AdminToken = %q", got.AdminToken)
	}
}

func TestLoad_invalidPort(t *testing.T) {
	// 비수치 PORT는 부팅 실패(TS도 Number(PORT)=NaN이면 서버 기동 실패).
	t.Setenv("PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("Load() with invalid PORT: expected error, got nil")
	}
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"", defaultPort, false},
		{"8080", 8080, false},
		{"  8080  ", 8080, false},
		{"not-a-number", 0, true},
		{"0", 0, false},
	}
	for _, tc := range tests {
		got, err := parsePort(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parsePort(%q): expected error", tc.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePort(%q) error = %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parsePort(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestAdminTokenInsecure(t *testing.T) {
	tests := []struct {
		token string
		want  bool
	}{
		{"", true},
		{PlaceholderAdminToken, true},
		{"real-token", false},
	}
	for _, tc := range tests {
		if got := (Config{AdminToken: tc.token}).AdminTokenInsecure(); got != tc.want {
			t.Errorf("AdminTokenInsecure(%q) = %v, want %v", tc.token, got, tc.want)
		}
	}
}
