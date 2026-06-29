import { createHmac } from "node:crypto";

// lazyfga-21: 데모/테스트용 ZITADEL 서명 헬퍼(어댑터 삭제로 이전됨). 실제 ZITADEL과 동일하게
// **초(seconds)** 타임스탬프를 쓴다(pkg/actions/signing.go): HMAC-SHA256("<unixSeconds>.<body>") hex,
// 헤더 형식 `t=<unixSeconds>,v1=<hex>`. signature.ts의 ZITADEL preset이 검증하는 형식과 일치한다.

/** HMAC-SHA256( secret, `<unixSeconds>.<rawBody>` ) hex. */
export function computeZitadelSignature(rawBody: Uint8Array, secret: string, unixSeconds: number): string {
  const h = createHmac("sha256", secret);
  h.update(String(unixSeconds));
  h.update(".");
  h.update(rawBody);
  return h.digest("hex");
}

/** `ZITADEL-Signature` 헤더 값 생성. 형식: "t=<unixSeconds>,v1=<hex>". */
export function zitadelSignatureHeader(rawBody: Uint8Array, secret: string, unixSeconds: number): string {
  return `t=${unixSeconds},v1=${computeZitadelSignature(rawBody, secret, unixSeconds)}`;
}
