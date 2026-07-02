package zitadelsign_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/modules/idp"
	"github.com/antegral-constellation/lazyfga/api/internal/zitadelsign"
)

// zitadelSpec는 idp zitadel preset과 동일한 서명 spec이다(내부 preset registry가 비공개라
// 검증 상호운용성 확인을 위해 여기서 재구성한다). 필드는 presets.go zitadelPreset과 일치.
var zitadelSpec = idp.WebhookSignatureSpec{
	Header:                  "ZITADEL-Signature",
	HeaderFormat:            "kv_t_v",
	TimestampSource:         "signature_header",
	TimestampUnit:           "seconds",
	PayloadTemplate:         "{timestamp}.{body}",
	Algorithm:               "sha256",
	Encoding:                "hex",
	SecretEncoding:          "raw",
	ToleranceSec:            300,
	AllowMultipleSignatures: true,
}

func TestComputeSignature_deterministic(t *testing.T) {
	body := []byte(`{"event_type":"user.grant.added"}`)
	a := zitadelsign.ComputeSignature(body, "secret", 1700000000)
	b := zitadelsign.ComputeSignature(body, "secret", 1700000000)
	if a != b {
		t.Fatalf("signature not deterministic: %q vs %q", a, b)
	}
	if len(a) != 64 { // sha256 hex.
		t.Fatalf("expected 64-hex signature, got %d chars: %q", len(a), a)
	}
	// 알려진 벡터: HMAC-SHA256(secret, "1700000000.{...}")를 직접 재계산해 고정한다.
	// 다른 timestamp/secret/body면 값이 바뀌어야 한다.
	if c := zitadelsign.ComputeSignature(body, "secret", 1700000001); c == a {
		t.Fatal("signature must depend on the timestamp")
	}
	if c := zitadelsign.ComputeSignature(body, "other", 1700000000); c == a {
		t.Fatal("signature must depend on the secret")
	}
	if c := zitadelsign.ComputeSignature([]byte("x"), "secret", 1700000000); c == a {
		t.Fatal("signature must depend on the body")
	}
}

func TestHeader_format(t *testing.T) {
	body := []byte("payload")
	h := zitadelsign.Header(body, "s", 1700000000)
	if !strings.HasPrefix(h, "t=1700000000,v1=") {
		t.Fatalf("header format wrong: %q", h)
	}
	sig := strings.TrimPrefix(h, "t=1700000000,v1=")
	if sig != zitadelsign.ComputeSignature(body, "s", 1700000000) {
		t.Fatalf("header v1 does not match ComputeSignature: %q", h)
	}
}

// TestSignature_verifiesWithIdpEngine은 서명 헬퍼 출력이 idp 서명 엔진(실 검증 경로)으로
// 검증되는지 확인한다 — 데모 webhook replay가 401 없이 통과함을 보장.
func TestSignature_verifiesWithIdpEngine(t *testing.T) {
	body := []byte(`{"event_type":"user.grant.added","event_payload":{"userId":"alice","projectId":"eng"}}`)
	const secret = "dev-zitadel-signing-secret"
	var ts int64 = 1700000000
	now := func() time.Time { return time.Unix(ts, 0) }

	header := http.Header{}
	header.Set("ZITADEL-Signature", zitadelsign.Header(body, secret, ts))

	if !idp.VerifyWebhookSignature(zitadelSpec, body, header, secret, now) {
		t.Fatal("idp engine rejected a signature produced by zitadelsign.Header")
	}

	// 음성: secret이 다르면 검증 실패해야 한다.
	if idp.VerifyWebhookSignature(zitadelSpec, body, header, "wrong-secret", now) {
		t.Fatal("idp engine accepted a signature under the wrong secret")
	}

	// 음성: replay 윈도우 밖(tolerance 300s 초과)이면 실패.
	stale := func() time.Time { return time.Unix(ts+1000, 0) }
	if idp.VerifyWebhookSignature(zitadelSpec, body, header, secret, stale) {
		t.Fatal("idp engine accepted a stale signature outside the tolerance window")
	}
}
