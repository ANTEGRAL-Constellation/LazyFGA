// Package config는 TS 백엔드 config.ts와 동일한 환경변수/기본값을 파싱한다.
// compose/.env로 주입되며 애플리케이션 전역의 단일 소스다.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PlaceholderAdminToken은 compose 예시가 쓰는 알려진 자리표시자다.
// 이 값이 그대로면 사실상 미설정과 같으므로 부팅 시 경고한다(LFGA-22 §4.4-4).
const PlaceholderAdminToken = "changeme-admin-token"

const (
	defaultPort          = 8787
	defaultDatabaseURL   = "postgres://lazyfga:lazyfga@localhost:5432/lazyfga"
	defaultOpenFGAAPIURL = "http://localhost:8080"
)

// Config는 런타임 설정 스냅샷이다.
type Config struct {
	Port          int
	DatabaseURL   string
	OpenFGAAPIURL string
	// StoreID는 선택. 빈 문자열이면 미지정으로 보고 부트스트랩이 store를 생성한다.
	StoreID string
	// AdminToken은 control-plane admin 토큰(비어 있으면 admin 라우트 도달 불가).
	AdminToken string
}

// Load는 환경변수에서 설정을 읽는다. config.ts의 기본값과 동일하다.
// TS `env ?? default`는 "미설정"일 때만 기본값을 쓰고 명시적 빈 문자열은 보존하므로
// LookupEnv로 동일하게 구분한다(빈 DATABASE_URL을 조용히 기본 DB로 바꾸지 않는다).
func Load() (Config, error) {
	port, err := parsePort(lookupOr("PORT", ""))
	if err != nil {
		return Config{}, err
	}
	return Config{
		Port:          port,
		DatabaseURL:   lookupOr("DATABASE_URL", defaultDatabaseURL),
		OpenFGAAPIURL: lookupOr("OPENFGA_API_URL", defaultOpenFGAAPIURL),
		// `LAZYFGA_STORE_ID || undefined`와 동일: 빈 문자열은 미지정으로 취급.
		StoreID: os.Getenv("LAZYFGA_STORE_ID"),
		// `ADMIN_TOKEN ?? ""`와 동일: 미설정이면 빈 문자열.
		AdminToken: os.Getenv("ADMIN_TOKEN"),
	}, nil
}

// AdminTokenInsecure는 admin 토큰이 비었거나 자리표시자와 같은지 보고한다.
func (c Config) AdminTokenInsecure() bool {
	return c.AdminToken == "" || c.AdminToken == PlaceholderAdminToken
}

// lookupOr는 미설정일 때만 기본값을 쓴다(`env ?? default` 대응; 명시적 "" 보존).
func lookupOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// parsePort는 PORT를 파싱한다. 미설정/빈 값이면 기본 포트, 비수치면 오류로 부팅을 실패시킨다
// (TS도 `Number(PORT)`가 NaN이면 Bun 서버가 기동에 실패한다). 빈 문자열→8787 폴백만이
// TS(""→port 0)와 다른 승인 편차다(LFGA-22 §4.4-4).
func parsePort(raw string) (int, error) {
	if raw == "" {
		return defaultPort, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("config: invalid PORT %q: %w", raw, err)
	}
	return n, nil
}
