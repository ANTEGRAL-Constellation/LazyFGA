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
  // 정수 ms만 허용(float t로 인한 서명 페이로드 경계 모호성 제거; 방어적).
  if (!Number.isInteger(t) || typeof parts.v1 !== "string" || parts.v1 === "") return null;
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
  // 첫 string·non-empty 후보 선택. `??`는 숫자/빈문자 후보에서 폴백을 끊어 유효한 후속 후보가
  // 있어도 이벤트를 통째로 누락시킨다(누락된 revocation → stale 권한). pick으로 그 버그를 막는다.
  const pick = (...vals: unknown[]): string | undefined =>
    vals.find((v): v is string => typeof v === "string" && v !== "");
  const type = pick(b.eventType, b.type);
  const userId = pick(b.aggregateID, b.userID, aggregate.id);
  if (type === undefined || userId === undefined) return null;

  const payload = (b.payload ?? {}) as Record<string, unknown>;
  const attributes: Record<string, string> = {};
  // string/number/boolean만 수용. array/object는 건너뛴다(`[a,b]`→"a,b" 같은 구조화 값이 tuple id로 새지 않게).
  const put = (key: string, v: unknown): void => {
    if (typeof v === "string") attributes[key] = v;
    else if (typeof v === "number" || typeof v === "boolean") attributes[key] = String(v);
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
