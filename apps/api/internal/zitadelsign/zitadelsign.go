// Package zitadelsign은 데모/테스트용 ZITADEL webhook 서명 헬퍼다
// (TS apps/api/scripts/lib/zitadel-sign.ts 포팅). 실제 ZITADEL과 동일하게 **초(seconds)**
// 타임스탬프를 쓴다(pkg/actions/signing.go): HMAC-SHA256("<unixSeconds>.<body>") hex,
// 헤더 형식 `t=<unixSeconds>,v1=<hex>`. idp 패키지의 zitadel preset(signature_header·seconds·
// "{timestamp}.{body}"·hex·raw secret)이 검증하는 형식과 일치한다.
package zitadelsign

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// ComputeSignature는 HMAC-SHA256(secret, "<unixSeconds>.<rawBody>")의 hex를 반환한다.
// secret은 raw 바이트로 HMAC 키가 된다(zitadel preset SecretEncoding="raw").
func ComputeSignature(rawBody []byte, secret string, unixSeconds int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(unixSeconds, 10)))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

// Header는 `ZITADEL-Signature` 헤더 값을 만든다. 형식: "t=<unixSeconds>,v1=<hex>".
func Header(rawBody []byte, secret string, unixSeconds int64) string {
	return "t=" + strconv.FormatInt(unixSeconds, 10) + ",v1=" + ComputeSignature(rawBody, secret, unixSeconds)
}
