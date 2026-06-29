import { createHmac, timingSafeEqual } from "node:crypto";
import type { IdpAdapter, IdpEvent } from "../types";

// lazyfga-16: ZITADEL Actions V2 adapter.
// ZITADEL은 외부 webhook 호출 시 `ZITADEL-Signature` 헤더에 콘텐츠+타임스탬프 기반 HMAC을 싣고,
// Target마다 Signing Key를 발급한다. 아래 computeSignature는 ZITADEL의 HMAC 구성을 따른다.
// (정확한 바이트 구성은 실제 ZITADEL `actions.ValidateRequestPayload`로 배포 시 확정한다.
//  데모 replay(lazyfga-19)는 이 동일 헬퍼로 서명해 라이브 ZITADEL 없이 자체 완결로 동작한다.)

/** replay 허용 윈도우(기본 5분). */
export const SIGNATURE_TOLERANCE_MS = 5 * 60 * 1000;

/** HMAC-SHA256( secret, `<timestampMs>.<rawBody>` ) hex. */
export function computeSignature(rawBody: Uint8Array, secret: string, timestamp: number): string {
  const h = createHmac("sha256", secret);
  h.update(String(timestamp));
  h.update(".");
  h.update(rawBody);
  return h.digest("hex");
}

/** `ZITADEL-Signature` 헤더 값 생성(데모 replay·테스트용). 형식: "t=<ms>,v1=<hex>". */
export function signatureHeader(rawBody: Uint8Array, secret: string, timestamp: number): string {
  return `t=${timestamp},v1=${computeSignature(rawBody, secret, timestamp)}`;
}

function parseSignatureHeader(value: string | null): { t: number; v1: string } | null {
  if (!value) return null;
  const parts: Record<string, string> = {};
  for (const seg of value.split(",")) {
    const i = seg.indexOf("=");
    if (i < 0) continue;
    parts[seg.slice(0, i).trim()] = seg.slice(i + 1).trim();
  }
  const t = Number(parts.t);
  if (!Number.isFinite(t) || typeof parts.v1 !== "string" || parts.v1 === "") return null;
  return { t, v1: parts.v1 };
}

function hexEqual(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  try {
    return timingSafeEqual(Buffer.from(a, "hex"), Buffer.from(b, "hex"));
  } catch {
    return false;
  }
}

/** ZITADEL 이벤트 payload(이벤트 트리거)에서 정규 IdpEvent를 추출(관대하게). */
function extractEvent(body: unknown): IdpEvent | null {
  if (body === null || typeof body !== "object") return null;
  const b = body as Record<string, unknown>;
  const aggregate = (b.aggregate ?? {}) as Record<string, unknown>;
  const type = (b.eventType ?? b.type) as unknown;
  const userId = (b.aggregateID ?? b.userID ?? aggregate.id) as unknown;
  if (typeof type !== "string" || typeof userId !== "string" || userId === "") return null;

  const payload = (b.payload ?? {}) as Record<string, unknown>;
  const attributes: Record<string, string> = {};
  const put = (key: string, v: unknown): void => {
    if (v !== undefined && v !== null) attributes[key] = String(v);
  };
  // 데모 seed는 projectId 기반(추가/삭제 대칭). roleKeys는 선택 확장에서만 사용.
  put("projectId", payload.projectID ?? payload.projectId);
  put("grantId", payload.grantID ?? payload.grantId ?? payload.projectGrantID);
  put("orgId", payload.orgID ?? payload.orgId ?? aggregate.resourceOwner);
  put("username", payload.userName ?? payload.username);
  return { type, subject: { id: userId }, attributes };
}

export const zitadelAdapter: IdpAdapter = {
  provider: "zitadel",

  verifySignature(rawBody, headers, secret) {
    const parsed = parseSignatureHeader(headers.get("zitadel-signature"));
    if (!parsed) return false;
    if (Math.abs(Date.now() - parsed.t) > SIGNATURE_TOLERANCE_MS) return false; // replay 방지
    return hexEqual(computeSignature(rawBody, secret, parsed.t), parsed.v1);
  },

  parseEvents(body) {
    const ev = extractEvent(body);
    return ev ? [ev] : [];
  },
};
