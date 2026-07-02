package idp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testNowSec = int64(1_700_000_000)

func fixedNow() time.Time { return time.Unix(testNowSec, 0) }

func encJSON(o any) []byte {
	b, _ := json.Marshal(o)
	return b
}

func hmacHex(secret string, parts ...[]byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	for _, p := range parts {
		mac.Write(p)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func hmacB64(key []byte, parts ...[]byte) string {
	mac := hmac.New(sha256.New, key)
	for _, p := range parts {
		mac.Write(p)
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func hdr(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Set(pairs[i], pairs[i+1])
	}
	return h
}

var sigBody = encJSON(map[string]any{
	"event_type":    "user.grant.added",
	"event_payload": map[string]any{"userId": "alice"},
})

// ── ZITADEL (kv_t_v, seconds, hex, raw secret) — preset에서 직접 ──

func zitadelSign(body []byte, secret string, t int64) string {
	return "t=" + strconv.FormatInt(t, 10) + ",v1=" + hmacHex(secret, []byte(strconv.FormatInt(t, 10)+"."), body)
}

func TestVerify_ZitadelPreset(t *testing.T) {
	spec := zitadelPreset.Signature
	const secret = "topsecret"

	t.Run("accepts a freshly signed request", func(t *testing.T) {
		h := hdr("ZITADEL-Signature", zitadelSign(sigBody, secret, testNowSec))
		if !VerifyWebhookSignature(spec, sigBody, h, secret, fixedNow) {
			t.Fatal("expected valid signature to verify")
		}
	})

	t.Run("rejects tampered body / wrong secret", func(t *testing.T) {
		h := hdr("ZITADEL-Signature", zitadelSign(sigBody, secret, testNowSec))
		if VerifyWebhookSignature(spec, encJSON(map[string]any{"evil": 1}), h, secret, fixedNow) {
			t.Error("tampered body must fail")
		}
		if VerifyWebhookSignature(spec, sigBody, h, "other", fixedNow) {
			t.Error("wrong secret must fail")
		}
	})

	t.Run("rejects stale and far-future timestamps (both-sided)", func(t *testing.T) {
		if VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", zitadelSign(sigBody, secret, testNowSec-301)), secret, fixedNow) {
			t.Error("stale must fail")
		}
		if VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", zitadelSign(sigBody, secret, testNowSec+301)), secret, fixedNow) {
			t.Error("far-future must fail")
		}
	})

	t.Run("accepts the tolerance boundary (300s)", func(t *testing.T) {
		if !VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", zitadelSign(sigBody, secret, testNowSec-300)), secret, fixedNow) {
			t.Error("exactly 300s stale must pass")
		}
	})

	t.Run("rejects float timestamp / missing / empty-or-nonhex v1", func(t *testing.T) {
		ts := strconv.FormatInt(testNowSec, 10)
		if VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", "t="+ts+".5,v1="+strings.Repeat("a", 64)), secret, fixedNow) {
			t.Error("float ts must fail")
		}
		if VerifyWebhookSignature(spec, sigBody, http.Header{}, secret, fixedNow) {
			t.Error("missing header must fail")
		}
		if VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", "t="+ts+",v1="), secret, fixedNow) {
			t.Error("empty v1 must fail")
		}
		if VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", "t="+ts+",v1=zzzz"), secret, fixedNow) {
			t.Error("non-hex v1 must fail")
		}
	})

	t.Run("rejects non-strict-decimal timestamps even with a valid HMAC", func(t *testing.T) {
		ts := strconv.FormatInt(testNowSec, 10)
		good := hmacHex(secret, []byte(ts+"."), sigBody)
		for _, bad := range []string{"t=" + ts + ".0,v1=" + good, "t=" + ts + "e0,v1=" + good, "t=0" + ts + ",v1=" + good} {
			if VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", bad), secret, fixedNow) {
				t.Errorf("non-strict-decimal ts must fail: %q", bad)
			}
		}
	})

	t.Run("accepts when one of multiple v1 signatures matches (key rotation)", func(t *testing.T) {
		ts := strconv.FormatInt(testNowSec, 10)
		good := hmacHex(secret, []byte(ts+"."), sigBody)
		h := hdr("ZITADEL-Signature", "t="+ts+",v1="+strings.Repeat("0", 64)+",v1="+good)
		if !VerifyWebhookSignature(spec, sigBody, h, secret, fixedNow) {
			t.Error("second matching v1 must verify")
		}
	})

	t.Run("header name is case-insensitive", func(t *testing.T) {
		h := hdr("zitadel-signature", zitadelSign(sigBody, secret, testNowSec))
		if !VerifyWebhookSignature(spec, sigBody, h, secret, fixedNow) {
			t.Error("lowercase header must verify")
		}
	})

	t.Run("missing t in kv header (signature_header source) fails", func(t *testing.T) {
		good := hmacHex(secret, []byte(strconv.FormatInt(testNowSec, 10)+"."), sigBody)
		if VerifyWebhookSignature(spec, sigBody, hdr("ZITADEL-Signature", "v1="+good), secret, fixedNow) {
			t.Error("missing t must fail")
		}
	})
}

// ── Stripe-style (kv_t_v) — ad-hoc spec, 같은 엔진 ──

func TestVerify_StripeStyle(t *testing.T) {
	spec := WebhookSignatureSpec{
		Header: "Stripe-Signature", HeaderFormat: "kv_t_v", TimestampSource: "signature_header",
		TimestampUnit: "seconds", PayloadTemplate: "{timestamp}.{body}", Algorithm: "sha256",
		Encoding: "hex", SecretEncoding: "raw", ToleranceSec: 300, AllowMultipleSignatures: true,
	}
	const secret = "whsec_stripe"
	ts := strconv.FormatInt(testNowSec, 10)
	sig := hmacHex(secret, []byte(ts+"."), sigBody)
	if !VerifyWebhookSignature(spec, sigBody, hdr("Stripe-Signature", "t="+ts+",v1="+sig), secret, fixedNow) {
		t.Error("valid Stripe-style signature must verify")
	}
}

// ── Standard Webhooks (base64, separate ts header, whsec_ base64 secret) — preset ──

func TestVerify_StandardWebhooksPreset(t *testing.T) {
	spec := standardWebhooksPreset.Signature
	const secret = "whsec_c2VjcmV0a2V5" // base64("secretkey") after prefix
	key, _ := base64.StdEncoding.DecodeString("c2VjcmV0a2V5")
	swBody := encJSON(map[string]any{"type": "user.created", "data": map[string]any{"id": "u1"}})

	sign := func(id string, ts int64) http.Header {
		sig := hmacB64(key, []byte(id+"."+strconv.FormatInt(ts, 10)+"."), swBody)
		return hdr("webhook-id", id, "webhook-timestamp", strconv.FormatInt(ts, 10), "webhook-signature", "v1,"+sig)
	}

	t.Run("accepts a valid Standard Webhooks signature", func(t *testing.T) {
		if !VerifyWebhookSignature(spec, swBody, sign("msg_1", testNowSec), secret, fixedNow) {
			t.Error("valid SW signature must verify")
		}
	})
	t.Run("rejects wrong id (id is part of the signed payload)", func(t *testing.T) {
		h := sign("msg_1", testNowSec)
		h.Set("webhook-id", "msg_tampered")
		if VerifyWebhookSignature(spec, swBody, h, secret, fixedNow) {
			t.Error("tampered id must fail")
		}
	})
	t.Run("rejects stale timestamp", func(t *testing.T) {
		if VerifyWebhookSignature(spec, swBody, sign("msg_1", testNowSec-301), secret, fixedNow) {
			t.Error("stale ts must fail")
		}
	})
	t.Run("rejects missing timestamp header", func(t *testing.T) {
		h := sign("msg_1", testNowSec)
		h.Del("webhook-timestamp")
		if VerifyWebhookSignature(spec, swBody, h, secret, fixedNow) {
			t.Error("missing ts header must fail")
		}
	})
	t.Run("rejects missing id header (id required for payload)", func(t *testing.T) {
		h := sign("msg_1", testNowSec)
		h.Del("webhook-id")
		if VerifyWebhookSignature(spec, swBody, h, secret, fixedNow) {
			t.Error("missing id header must fail")
		}
	})
	t.Run("rejects non-decimal separate timestamp header", func(t *testing.T) {
		h := sign("msg_1", testNowSec)
		h.Set("webhook-timestamp", "not-a-number")
		if VerifyWebhookSignature(spec, swBody, h, secret, fixedNow) {
			t.Error("non-decimal ts header must fail")
		}
	})
}

// ── GitHub (scheme_hex, no timestamp) — ad-hoc spec ──

func TestVerify_GitHubStyle(t *testing.T) {
	spec := WebhookSignatureSpec{
		Header: "X-Hub-Signature-256", HeaderFormat: "scheme_hex", TimestampSource: "none",
		TimestampUnit: "none", PayloadTemplate: "{body}", Algorithm: "sha256", Encoding: "hex",
		SecretEncoding: "raw", ToleranceSec: 300, AllowMultipleSignatures: false,
	}
	const secret = "ghsecret"
	sig := hmacHex(secret, sigBody)

	t.Run("accepts sha256=<hex> over the raw body", func(t *testing.T) {
		if !VerifyWebhookSignature(spec, sigBody, hdr("X-Hub-Signature-256", "sha256="+sig), secret, fixedNow) {
			t.Error("valid scheme_hex must verify")
		}
	})
	t.Run("rejects a tampered body", func(t *testing.T) {
		if VerifyWebhookSignature(spec, encJSON(map[string]any{"evil": 1}), hdr("X-Hub-Signature-256", "sha256="+sig), secret, fixedNow) {
			t.Error("tampered body must fail")
		}
	})
	t.Run("rejects a mismatched scheme label (md5= not compared as sha256)", func(t *testing.T) {
		if VerifyWebhookSignature(spec, sigBody, hdr("X-Hub-Signature-256", "md5="+sig), secret, fixedNow) {
			t.Error("mismatched scheme must fail")
		}
	})
	t.Run("rejects header without = separator", func(t *testing.T) {
		if VerifyWebhookSignature(spec, sigBody, hdr("X-Hub-Signature-256", "sha256"+sig), secret, fixedNow) {
			t.Error("no separator must fail")
		}
	})
	t.Run("rejects empty signature after scheme", func(t *testing.T) {
		if VerifyWebhookSignature(spec, sigBody, hdr("X-Hub-Signature-256", "sha256="), secret, fixedNow) {
			t.Error("empty sig must fail")
		}
	})
}

// ── 추가 분기 커버 ──

func TestVerify_AllowMultipleFalse_KeepsFirstOnly(t *testing.T) {
	spec := WebhookSignatureSpec{
		Header: "Sig", HeaderFormat: "kv_t_v", TimestampSource: "signature_header",
		TimestampUnit: "seconds", PayloadTemplate: "{timestamp}.{body}", Algorithm: "sha256",
		Encoding: "hex", SecretEncoding: "raw", ToleranceSec: 300, AllowMultipleSignatures: false,
	}
	const secret = "s"
	ts := strconv.FormatInt(testNowSec, 10)
	good := hmacHex(secret, []byte(ts+"."), sigBody)
	// 첫 v1은 틀리고 둘째가 맞지만, allowMultiple=false면 첫 것만 검사 → 실패.
	h := hdr("Sig", "t="+ts+",v1="+strings.Repeat("0", 64)+",v1="+good)
	if VerifyWebhookSignature(spec, sigBody, h, secret, fixedNow) {
		t.Error("with allowMultiple=false only the first signature is checked")
	}
}

func TestVerify_MillisUnit(t *testing.T) {
	spec := WebhookSignatureSpec{
		Header: "Sig", HeaderFormat: "kv_t_v", TimestampSource: "signature_header",
		TimestampUnit: "millis", PayloadTemplate: "{timestamp}.{body}", Algorithm: "sha256",
		Encoding: "hex", SecretEncoding: "raw", ToleranceSec: 300, AllowMultipleSignatures: true,
	}
	const secret = "s"
	tsMillis := strconv.FormatInt(testNowSec*1000, 10)
	sig := hmacHex(secret, []byte(tsMillis+"."), sigBody)
	if !VerifyWebhookSignature(spec, sigBody, hdr("Sig", "t="+tsMillis+",v1="+sig), secret, fixedNow) {
		t.Error("millis-unit fresh timestamp must verify")
	}
	// 301초 초과(밀리초)면 실패.
	tsStale := strconv.FormatInt((testNowSec-301)*1000, 10)
	sigStale := hmacHex(secret, []byte(tsStale+"."), sigBody)
	if VerifyWebhookSignature(spec, sigBody, hdr("Sig", "t="+tsStale+",v1="+sigStale), secret, fixedNow) {
		t.Error("stale millis timestamp must fail")
	}
}

func TestVerify_Base64SecretRawEncoding(t *testing.T) {
	// secretEncoding=base64 + prefix strip + 무패딩 base64.
	spec := WebhookSignatureSpec{
		Header: "Sig", HeaderFormat: "scheme_hex", TimestampSource: "none", TimestampUnit: "none",
		PayloadTemplate: "{body}", Algorithm: "sha256", Encoding: "base64", SecretEncoding: "base64",
		SecretPrefix: "pre_", ToleranceSec: 300, AllowMultipleSignatures: false,
	}
	rawKey := []byte("key-material")
	b64 := base64.StdEncoding.EncodeToString(rawKey)
	secret := "pre_" + b64
	sig := hmacB64(rawKey, sigBody)
	if !VerifyWebhookSignature(spec, sigBody, hdr("Sig", "sha256="+sig), secret, fixedNow) {
		t.Error("base64 secret with prefix must verify")
	}
}

func TestVerify_SeparateHeaderMissingTimestampHeaderName(t *testing.T) {
	// timestampHeader 이름 미지정 → 타임스탬프 부재로 취급 → 실패.
	spec := WebhookSignatureSpec{
		Header: "webhook-signature", HeaderFormat: "standard_webhooks", TimestampSource: "separate_header",
		TimestampUnit: "seconds", PayloadTemplate: "{timestamp}.{body}", Algorithm: "sha256",
		Encoding: "base64", SecretEncoding: "raw", ToleranceSec: 300, AllowMultipleSignatures: true,
	}
	if VerifyWebhookSignature(spec, sigBody, hdr("webhook-signature", "v1,abc"), "s", fixedNow) {
		t.Error("separate_header without a header name must fail")
	}
}
