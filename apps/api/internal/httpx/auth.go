package httpx

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/modules/auth"
)

// Role은 인증 principal의 역할이다.
type Role string

const (
	RoleAdmin   Role = "admin"
	RoleService Role = "service"
)

// Principal은 인증된 호출자다.
type Principal struct {
	Role    Role
	TokenID string
}

// ErrUnauthorized는 인증 실패(401)를 나타내는 sentinel이다. 인프라 오류는 이 값이 아니며
// 별도로 전파되어 500이 된다(401로 가리지 않음).
var ErrUnauthorized = errors.New("unauthorized")

// Authenticator는 Authorization 헤더를 Principal로 해석한다.
type Authenticator interface {
	Authenticate(ctx context.Context, authorizationHeader string) (Principal, error)
}

// ServiceTokenRepo는 인증 미들웨어가 필요로 하는 토큰 저장소 연산이다.
type ServiceTokenRepo interface {
	FindActiveByHash(ctx context.Context, tokenHash string) (*auth.ServiceToken, error)
	TouchLastUsed(ctx context.Context, id string) error
}

// TokenAuthenticator는 admin 토큰 + service 토큰 3티어 인증을 구현한다.
type TokenAuthenticator struct {
	adminToken string
	repo       ServiceTokenRepo
	// touch는 last_used_at 갱신 디스패치(테스트 주입 지점). 기본은 goroutine.
	touch func(func())
}

// NewTokenAuthenticator는 인증기를 만든다.
func NewTokenAuthenticator(adminToken string, repo ServiceTokenRepo) *TokenAuthenticator {
	return &TokenAuthenticator{
		adminToken: adminToken,
		repo:       repo,
		touch:      func(fn func()) { go fn() },
	}
}

var bearerRe = regexp.MustCompile(`(?i)^Bearer\s+(.+)$`)

// parseBearer는 `Bearer <token>`(대소문자 무시)에서 토큰을 추출한다. 실패 시 "".
func parseBearer(header string) string {
	m := bearerRe.FindStringSubmatch(strings.TrimSpace(header))
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// Authenticate는 Bearer 토큰을 Principal로 해석한다.
// 실패는 ErrUnauthorized, 인프라 오류는 원본 오류(→ 500)로 반환한다.
func (a *TokenAuthenticator) Authenticate(ctx context.Context, header string) (Principal, error) {
	token := parseBearer(header)
	if token == "" {
		return Principal{}, ErrUnauthorized
	}

	if a.adminToken != "" && constantTimeEqual(auth.Sha256Hex(token), auth.Sha256Hex(a.adminToken)) {
		return Principal{Role: RoleAdmin}, nil
	}

	row, err := a.repo.FindActiveByHash(ctx, auth.Sha256Hex(token))
	if err != nil {
		return Principal{}, err // 인프라 장애 → 전파(500), 401로 가리지 않음.
	}
	if row != nil {
		a.touch(func() { _ = a.repo.TouchLastUsed(context.Background(), row.ID) }) // best-effort.
		return Principal{Role: RoleService, TokenID: row.ID}, nil
	}
	return Principal{}, ErrUnauthorized
}

// constantTimeEqual은 같은 길이(64 hex)의 두 문자열을 상수시간 비교한다.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RequireRole은 라우트 그룹을 가드한다. 미인증=401, 역할 부족=403, 인프라 오류=500.
// 성공 시 Principal을 요청 컨텍스트에 실어 다음 핸들러로 전달한다.
func RequireRole(a Authenticator, roles ...Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, err := a.Authenticate(r.Context(), r.Header.Get("Authorization"))
			if err != nil {
				if errors.Is(err, ErrUnauthorized) {
					WriteError(w, http.StatusUnauthorized, "unauthorized")
					return
				}
				// 인프라 장애: TS는 rethrow → Hono 기본 onError("Internal Server Error", text/plain).
				WriteHonoInternalError(w)
				return
			}
			if !roleAllowed(p.Role, roles) {
				WriteError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r.WithContext(ContextWithPrincipal(r.Context(), p)))
		})
	}
}

func roleAllowed(role Role, allowed []Role) bool {
	for _, r := range allowed {
		if r == role {
			return true
		}
	}
	return false
}

// principalCtxKey는 컨텍스트에 principal을 싣기 위한 타입 키다(Hono c.set("principal") 대응).
type principalCtxKey struct{}

// ContextWithPrincipal은 principal을 컨텍스트에 싣는다.
func ContextWithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFromContext는 컨텍스트에서 principal을 꺼낸다.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}
