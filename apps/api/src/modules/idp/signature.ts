import { createHmac, timingSafeEqual } from "node:crypto";

// lazyfga-21: 설정형 webhook 서명 검증 엔진. provider별 코드 대신 WebhookSignatureSpec 하나로
// ZITADEL / Stripe-style / Standard Webhooks / GitHub 등을 모두 검증한다(순수, node:crypto).

export interface WebhookSignatureSpec {
  /** 서명 헤더 이름. 예: "ZITADEL-Signature", "webhook-signature". */
  header: string;
  /** 헤더 파싱 형식. */
  headerFormat: "kv_t_v" | "scheme_hex" | "standard_webhooks";
  /** 타임스탬프 출처. */
  timestampSource: "signature_header" | "separate_header" | "none";
  /** timestampSource="separate_header"일 때 타임스탬프 헤더 이름(예: "webhook-timestamp"). */
  timestampHeader?: string;
  /** 타임스탬프 단위. "millis"는 미래 대비(현재 preset 미사용). */
  timestampUnit: "seconds" | "millis" | "none";
  /** {id}를 쓰는 템플릿용 id 출처(예: Standard Webhooks "webhook-id"). */
  idSource?: { header: string };
  /** 서명 대상 페이로드 템플릿. placeholders: {body} {timestamp} {id}. */
  payloadTemplate: string;
  algorithm: "sha256";
  /** 헤더에 실린 서명 값의 인코딩. */
  encoding: "hex" | "base64";
  /** 저장된 secret을 HMAC 키 바이트로 디코딩하는 방식. */
  secretEncoding: "raw" | "base64";
  /** 디코딩 전 제거할 접두사(예: Standard Webhooks "whsec_"). */
  secretPrefix?: string;
  /** replay 허용 윈도우(초). too-old + (의도적 하드닝) far-future 모두 거부. */
  toleranceSec: number;
  /** 다중 서명 허용(키 회전). */
  allowMultipleSignatures: boolean;
}

/** 현재 시각(초). 테스트가 주입할 수 있게 분리(결정성). */
function nowSeconds(): number {
  return Math.floor(Date.now() / 1000);
}

/** 헤더에서 (timestamp 초, 서명값 목록)을 추출. 형식 위반 시 null. */
// 타임스탬프는 **raw 문자열**로 보존한다(서명 페이로드는 서명자가 쓴 그대로의 문자열을 써야 한다 —
// Number 라운드트립으로 "1782751299.0"·"...e0"·leading-zero를 정규화하면 검증이 어긋난다).
const DECIMAL_RE = /^[0-9]+$/;

function parseHeader(
  spec: WebhookSignatureSpec,
  headers: Headers,
): { timestampRaw?: string; signatures: string[] } | null {
  const raw = headers.get(spec.header);
  if (!raw) return null;

  let signatures: string[] = [];
  let inHeaderTsRaw: string | undefined;

  if (spec.headerFormat === "kv_t_v") {
    // "t=<ts>,v1=<sig>[,v1=<sig>]"
    const parts: Record<string, string[]> = {};
    for (const seg of raw.split(",")) {
      const i = seg.indexOf("=");
      if (i < 0) continue;
      const k = seg.slice(0, i).trim();
      const v = seg.slice(i + 1).trim();
      (parts[k] ??= []).push(v);
    }
    const tRaw = parts.t?.[0];
    if (tRaw !== undefined) {
      // 엄격한 10진 정수만(".0"/"e0"/leading-zero 거부 — ZITADEL strconv.ParseInt과 동일하게).
      if (!DECIMAL_RE.test(tRaw)) return null;
      inHeaderTsRaw = tRaw;
    }
    signatures = (parts.v1 ?? []).filter((s) => s !== "");
  } else if (spec.headerFormat === "scheme_hex") {
    // "sha256=<hex>" — scheme label은 알고리즘과 일치해야 한다(예: md5=...를 sha256으로 비교 방지).
    const eq = raw.indexOf("=");
    if (eq < 0) return null;
    if (raw.slice(0, eq).trim().toLowerCase() !== spec.algorithm) return null;
    const sig = raw.slice(eq + 1).trim();
    if (sig === "") return null;
    signatures = [sig];
  } else {
    // standard_webhooks: "v1,<base64>[ v1,<base64>]"
    for (const tok of raw.split(/\s+/)) {
      const comma = tok.indexOf(",");
      if (comma < 0) continue;
      const ver = tok.slice(0, comma);
      const sig = tok.slice(comma + 1);
      if (ver === "v1" && sig !== "") signatures.push(sig);
    }
  }

  if (signatures.length === 0) return null;
  if (!spec.allowMultipleSignatures) signatures = signatures.slice(0, 1);

  // 타임스탬프 출처 해석(raw 문자열로 보존).
  let timestampRaw: string | undefined;
  if (spec.timestampSource === "signature_header") {
    timestampRaw = inHeaderTsRaw;
    if (timestampRaw === undefined) return null;
  } else if (spec.timestampSource === "separate_header") {
    const tv = spec.timestampHeader ? headers.get(spec.timestampHeader) : null;
    if (tv === null || !DECIMAL_RE.test(tv)) return null;
    timestampRaw = tv;
  }
  return { timestampRaw, signatures };
}

/** spec.secretEncoding/secretPrefix에 따라 HMAC 키 바이트를 만든다. */
function hmacKey(spec: WebhookSignatureSpec, secret: string): Buffer {
  let s = secret;
  if (spec.secretPrefix && s.startsWith(spec.secretPrefix)) s = s.slice(spec.secretPrefix.length);
  return spec.secretEncoding === "base64" ? Buffer.from(s, "base64") : Buffer.from(s, "utf8");
}

/** 타임스탬프(초)를 toleranceSec 양방향 윈도우로 검사. */
function withinTolerance(spec: WebhookSignatureSpec, tsRaw: number): boolean {
  const ts = spec.timestampUnit === "millis" ? Math.floor(tsRaw / 1000) : tsRaw;
  return Math.abs(nowSeconds() - ts) <= spec.toleranceSec;
}

function encodedEqual(a: string, b: string, encoding: "hex" | "base64"): boolean {
  try {
    const ba = Buffer.from(a, encoding);
    const bb = Buffer.from(b, encoding);
    return ba.length > 0 && ba.length === bb.length && timingSafeEqual(ba, bb);
  } catch {
    return false;
  }
}

/**
 * webhook 서명 검증(provider-agnostic). 유효한 서명이 하나라도 맞으면 true.
 * 미서명/형식위반/시각초과/불일치 → false.
 */
export function verifyWebhookSignature(
  spec: WebhookSignatureSpec,
  rawBody: Uint8Array,
  headers: Headers,
  secret: string,
): boolean {
  const parsed = parseHeader(spec, headers);
  if (!parsed) return false;

  // replay 윈도우(타임스탬프가 있을 때만; "none"은 검사 안 함).
  if (spec.timestampSource !== "none") {
    if (parsed.timestampRaw === undefined) return false;
    if (!withinTolerance(spec, Number(parsed.timestampRaw))) return false;
  }

  // 서명 대상 페이로드 조립.
  const bodyStr = new TextDecoder().decode(rawBody);
  let signed = spec.payloadTemplate;
  if (signed.includes("{timestamp}")) {
    if (parsed.timestampRaw === undefined) return false;
    signed = signed.split("{timestamp}").join(parsed.timestampRaw);
  }
  if (signed.includes("{id}")) {
    const id = spec.idSource ? headers.get(spec.idSource.header) : null;
    if (id === null || id === "") return false;
    signed = signed.split("{id}").join(id);
  }
  signed = signed.split("{body}").join(bodyStr);

  const expected = createHmac(spec.algorithm, hmacKey(spec, secret))
    .update(signed)
    .digest(spec.encoding);
  return parsed.signatures.some((sig) => encodedEqual(expected, sig, spec.encoding));
}
