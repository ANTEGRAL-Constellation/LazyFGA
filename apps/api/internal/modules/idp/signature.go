package idp

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WebhookSignatureSpec는 설정형 webhook 서명 검증 spec이다(TS signature.ts 포팅).
// provider별 코드 대신 이 spec 하나로 ZITADEL / Stripe-style / Standard Webhooks / GitHub 등을
// 모두 검증한다(순수, crypto stdlib).
type WebhookSignatureSpec struct {
	// Header는 서명 헤더 이름. 예: "ZITADEL-Signature", "webhook-signature".
	Header string
	// HeaderFormat은 헤더 파싱 형식: "kv_t_v" | "scheme_hex" | "standard_webhooks".
	HeaderFormat string
	// TimestampSource는 타임스탬프 출처: "signature_header" | "separate_header" | "none".
	TimestampSource string
	// TimestampHeader는 TimestampSource="separate_header"일 때 타임스탬프 헤더 이름.
	TimestampHeader string
	// TimestampUnit은 타임스탬프 단위: "seconds" | "millis" | "none".
	TimestampUnit string
	// IDSource는 {id}를 쓰는 템플릿용 id 출처(예: Standard Webhooks "webhook-id").
	IDSource *IDSource
	// PayloadTemplate은 서명 대상 페이로드 템플릿. placeholders: {body} {timestamp} {id}.
	PayloadTemplate string
	// Algorithm은 "sha256".
	Algorithm string
	// Encoding은 헤더에 실린 서명 값의 인코딩: "hex" | "base64".
	Encoding string
	// SecretEncoding은 저장된 secret을 HMAC 키 바이트로 디코딩하는 방식: "raw" | "base64".
	SecretEncoding string
	// SecretPrefix는 디코딩 전 제거할 접두사(예: Standard Webhooks "whsec_").
	SecretPrefix string
	// ToleranceSec는 replay 허용 윈도우(초). too-old + far-future 모두 거부.
	ToleranceSec int
	// AllowMultipleSignatures는 다중 서명 허용(키 회전).
	AllowMultipleSignatures bool
}

// IDSource는 payload {id} 치환용 헤더 출처다.
type IDSource struct {
	Header string
}

// decimalRe는 엄격한 10진 정수(".0"/"e0"/leading-zero 거부)를 요구한다 —
// 타임스탬프를 raw 문자열로 보존하며 ZITADEL strconv.ParseInt과 동일하게 동작시킨다.
var decimalRe = regexp.MustCompile(`^[0-9]+$`)

// parsedSignature는 파싱된 (타임스탬프 raw, 서명값 목록)이다.
type parsedSignature struct {
	timestampRaw string // hasTimestamp=false면 무의미.
	hasTimestamp bool
	signatures   []string
}

// parseHeader는 헤더에서 (timestamp raw, 서명값 목록)을 추출한다. 형식 위반 시 ok=false.
func parseHeader(spec WebhookSignatureSpec, h http.Header) (parsedSignature, bool) {
	raw := h.Get(spec.Header)
	if raw == "" {
		return parsedSignature{}, false
	}

	var signatures []string
	inHeaderTsRaw := ""
	hasInHeaderTs := false

	switch spec.HeaderFormat {
	case "kv_t_v":
		// "t=<ts>,v1=<sig>[,v1=<sig>]"
		parts := map[string][]string{}
		for _, seg := range strings.Split(raw, ",") {
			i := strings.IndexByte(seg, '=')
			if i < 0 {
				continue
			}
			k := strings.TrimSpace(seg[:i])
			v := strings.TrimSpace(seg[i+1:])
			parts[k] = append(parts[k], v)
		}
		if ts := parts["t"]; len(ts) > 0 {
			// 엄격한 10진 정수만(".0"/"e0"/leading-zero 거부).
			if !decimalRe.MatchString(ts[0]) {
				return parsedSignature{}, false
			}
			inHeaderTsRaw = ts[0]
			hasInHeaderTs = true
		}
		for _, v := range parts["v1"] {
			if v != "" {
				signatures = append(signatures, v)
			}
		}
	case "scheme_hex":
		// "sha256=<hex>" — scheme label은 알고리즘과 일치해야 한다.
		eq := strings.IndexByte(raw, '=')
		if eq < 0 {
			return parsedSignature{}, false
		}
		if strings.ToLower(strings.TrimSpace(raw[:eq])) != spec.Algorithm {
			return parsedSignature{}, false
		}
		sig := strings.TrimSpace(raw[eq+1:])
		if sig == "" {
			return parsedSignature{}, false
		}
		signatures = []string{sig}
	default:
		// standard_webhooks: "v1,<base64>[ v1,<base64>]"
		for _, tok := range strings.Fields(raw) {
			comma := strings.IndexByte(tok, ',')
			if comma < 0 {
				continue
			}
			if tok[:comma] == "v1" && tok[comma+1:] != "" {
				signatures = append(signatures, tok[comma+1:])
			}
		}
	}

	if len(signatures) == 0 {
		return parsedSignature{}, false
	}
	if !spec.AllowMultipleSignatures {
		signatures = signatures[:1]
	}

	// 타임스탬프 출처 해석(raw 문자열로 보존).
	timestampRaw := ""
	hasTimestamp := false
	switch spec.TimestampSource {
	case "signature_header":
		if !hasInHeaderTs {
			return parsedSignature{}, false
		}
		timestampRaw = inHeaderTsRaw
		hasTimestamp = true
	case "separate_header":
		tv := ""
		if spec.TimestampHeader != "" {
			tv = h.Get(spec.TimestampHeader)
		}
		if !decimalRe.MatchString(tv) {
			return parsedSignature{}, false
		}
		timestampRaw = tv
		hasTimestamp = true
	}
	return parsedSignature{timestampRaw: timestampRaw, hasTimestamp: hasTimestamp, signatures: signatures}, true
}

// hmacKey는 spec.SecretEncoding/SecretPrefix에 따라 HMAC 키 바이트를 만든다.
func hmacKey(spec WebhookSignatureSpec, secret string) []byte {
	s := secret
	if spec.SecretPrefix != "" && strings.HasPrefix(s, spec.SecretPrefix) {
		s = s[len(spec.SecretPrefix):]
	}
	if spec.SecretEncoding == "base64" {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			return b
		}
		if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
			return b
		}
		return nil // 디코딩 불가 → 빈 키(검증 실패로 이어짐).
	}
	return []byte(s)
}

// withinTolerance는 타임스탬프(초)를 toleranceSec 양방향 윈도우로 검사한다.
func withinTolerance(spec WebhookSignatureSpec, tsRaw float64, now func() time.Time) bool {
	ts := tsRaw
	if spec.TimestampUnit == "millis" {
		ts = math.Floor(tsRaw / 1000)
	}
	return math.Abs(float64(now().Unix())-ts) <= float64(spec.ToleranceSec)
}

// decodeSig는 헤더 서명값을 인코딩에 맞게 바이트로 디코딩한다. 실패 시 ok=false.
func decodeSig(s, encoding string) ([]byte, bool) {
	if encoding == "hex" {
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, false
		}
		return b, true
	}
	// base64(패딩/무패딩 모두 허용 — Node Buffer.from(...,"base64")의 관대함 근사).
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	return nil, false
}

// VerifyWebhookSignature는 raw webhook 본문을 선언적 서명 spec으로 검증한다.
// 유효한 서명이 하나라도 맞으면 true. 미서명/형식위반/시각초과/불일치 → false.
// now는 replay 윈도우 검사용 주입 클록(프로덕션은 time.Now).
func VerifyWebhookSignature(spec WebhookSignatureSpec, rawBody []byte, h http.Header, secret string, now func() time.Time) bool {
	parsed, ok := parseHeader(spec, h)
	if !ok {
		return false
	}

	// replay 윈도우(타임스탬프가 있을 때만; "none"은 검사 안 함).
	if spec.TimestampSource != "none" {
		if !parsed.hasTimestamp {
			return false
		}
		tsNum, err := strconv.ParseFloat(parsed.timestampRaw, 64)
		if err != nil {
			return false
		}
		if !withinTolerance(spec, tsNum, now) {
			return false
		}
	}

	// 서명 대상 페이로드 조립(타임스탬프는 raw 문자열 그대로).
	signed := spec.PayloadTemplate
	if strings.Contains(signed, "{timestamp}") {
		if !parsed.hasTimestamp {
			return false
		}
		signed = strings.ReplaceAll(signed, "{timestamp}", parsed.timestampRaw)
	}
	if strings.Contains(signed, "{id}") {
		id := ""
		if spec.IDSource != nil {
			id = h.Get(spec.IDSource.Header)
		}
		if id == "" {
			return false
		}
		signed = strings.ReplaceAll(signed, "{id}", id)
	}
	signed = strings.ReplaceAll(signed, "{body}", string(rawBody))

	mac := hmac.New(sha256.New, hmacKey(spec, secret))
	mac.Write([]byte(signed))
	sum := mac.Sum(nil)

	// decode-then-constant-time compare(비어있지 않고 길이 같을 때만).
	for _, sig := range parsed.signatures {
		cand, ok := decodeSig(sig, spec.Encoding)
		if !ok {
			continue
		}
		if len(cand) == len(sum) && subtle.ConstantTimeCompare(cand, sum) == 1 {
			return true
		}
	}
	return false
}
