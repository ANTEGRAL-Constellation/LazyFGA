package auditread

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/antegral-constellation/lazyfga/api/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// QueryRepo는 라우트가 필요로 하는 조회 연산이다(*DBRepo가 만족).
type QueryRepo interface {
	Query(ctx context.Context, q Query) (Result, error)
}

// Deps는 감사 조회 라우트의 의존성이다.
type Deps struct {
	Repo QueryRepo
	Auth httpx.Authenticator
}

// Mount는 감사 조회 라우트(admin 전용)를 마운트한다.
func Mount(r chi.Router, d Deps) {
	// TS auditRoutes.use("*", admin): /audit 이하 전 경로 가드(미매칭 포함).
	r.Route("/audit", func(ar chi.Router) {
		ar.Use(httpx.RequireRole(d.Auth, httpx.RoleAdmin))
		ar.Use(httpx.TrailingSlash404)
		ar.Get("/", d.handleQuery)
	})
}

// queryResponse는 GET /audit 응답이다(nextCursor는 없으면 생략).
type queryResponse struct {
	Entries    []Entry `json:"entries"`
	NextCursor string  `json:"nextCursor,omitempty"`
}

func (d Deps) handleQuery(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	limit := parseLimit(qs.Get("limit"))

	var cursor *Cursor
	if cs := qs.Get("cursor"); cs != "" {
		c, ok := decodeCursor(cs)
		if !ok {
			httpx.WriteError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		cursor = c
	}

	from, fromOK := parseDateParam(qs, "from")
	to, toOK := parseDateParam(qs, "to")
	if !fromOK || !toOK {
		httpx.WriteError(w, http.StatusBadRequest, "invalid from/to (use ISO 8601)")
		return
	}

	res, err := d.Repo.Query(r.Context(), Query{
		Action: qs.Get("action"),
		Actor:  qs.Get("actor"),
		From:   from,
		To:     to,
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		httpx.WriteHonoInternalError(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, queryResponse(res))
}

// parseDateParam은 from/to 쿼리 파라미터를 파싱한다.
//   - 부재 → (nil, true): 필터 없음.
//   - 존재+유효 → (&t, true).
//   - 존재+무효(빈 값 포함) → (nil, false): 400.
func parseDateParam(qs url.Values, key string) (*time.Time, bool) {
	if !qs.Has(key) {
		return nil, true
	}
	t, ok := parseFlexibleTime(qs.Get(key))
	if !ok {
		return nil, false
	}
	return &t, true
}

// parseLimit은 TS `Math.min(Math.max(Math.trunc(Number(limit)) || 50, 1), 200)`을 재현한다.
func parseLimit(s string) int {
	t := math.Trunc(jsStringToNumber(s))
	if t == 0 || math.IsNaN(t) { // 0/-0/NaN → 50(JS `|| 50`).
		t = 50
	}
	if t < 1 {
		t = 1
	}
	if t > 200 {
		t = 200
	}
	return int(t)
}

// jsDecimalRe는 JS StrDecimalLiteral(부호+정수/소수/지수)이다.
var jsDecimalRe = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

// jsStringToNumber는 JS `Number(string)`을 근사한다(limit 파싱 parity).
func jsStringToNumber(s string) float64 {
	t := strings.TrimFunc(s, isJSSpace)
	if t == "" {
		return 0
	}
	switch t {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	// hex/octal/binary 정수 리터럴(부호 없음).
	if len(t) > 2 && t[0] == '0' {
		switch t[1] {
		case 'x', 'X':
			return parseRadix(t[2:], 16)
		case 'o', 'O':
			return parseRadix(t[2:], 8)
		case 'b', 'B':
			return parseRadix(t[2:], 2)
		}
	}
	if !jsDecimalRe.MatchString(t) {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(t, 64)
	if err != nil {
		// 범위 초과(예: 1e400)는 JS Number처럼 ±Infinity로 수렴한다(ParseFloat가 값과 함께 ErrRange를 준다).
		var ne *strconv.NumError
		if errors.As(err, &ne) && errors.Is(ne.Err, strconv.ErrRange) {
			return v
		}
		return math.NaN()
	}
	return v
}

func parseRadix(s string, base int) float64 {
	v, err := strconv.ParseUint(s, base, 64)
	if err == nil {
		return float64(v)
	}
	// 2^64 초과도 JS Number는 유한/무한 float로 계산한다 — 자릿수 누적으로 근사한다
	// (limit 클램프 목적상 크기만 맞으면 충분).
	var ne *strconv.NumError
	if !errors.As(err, &ne) || !errors.Is(ne.Err, strconv.ErrRange) {
		return math.NaN()
	}
	acc := 0.0
	for _, c := range s {
		d := digitVal(c)
		if d < 0 || d >= base {
			return math.NaN()
		}
		acc = acc*float64(base) + float64(d)
	}
	return acc
}

func digitVal(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// isJSSpace는 JS \s(공백 + 라인 종결자)와 동일한 집합이다(Number 앞뒤 트림).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0xfeff, 0x2028, 0x2029:
		return true
	}
	return unicode.Is(unicode.Zs, r)
}
