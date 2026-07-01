import { FgaApiError, FgaApiInternalError, FgaApiRateLimitExceededError } from "@openfga/sdk";

// OpenFGA write/delete 오류 분류(lazyfga-15 hardening). idp(웹훅 동기화)·permission(lazyfga-20
// grant/revoke)이 공유한다. 분류 의미는 두 곳에서 동일해야 하므로 단일 모듈로 둔다.

const TRANSIENT_CODES = new Set([
  "ECONNREFUSED",
  "ENOTFOUND",
  "ECONNRESET",
  "ETIMEDOUT",
  "EAI_AGAIN",
]);

/**
 * 일시적(재시도 가능) OpenFGA 오류인가. **HTTP status 기준**:
 * - 5xx/429, 또는 HTTP 응답이 없음(statusCode undefined = 네트워크 단절) → transient.
 *   ECONNRESET/소켓 끊김 등은 SDK가 .code를 보존하지 않으므로 statusCode 부재로 잡는다(FgaApiError 포함).
 * - 4xx(결정적): **메시지 free-text로는 절대 transient 판정하지 않는다**(이벤트 값이 'timeout' 등을
 *   품어 무한재시도 유발하는 것 방지).
 * write/delete/read가 공유한다(transient → 502, 그 외 → 결정적 처리).
 */
export function isTransientApiError(e: unknown): boolean {
  if (e instanceof FgaApiInternalError || e instanceof FgaApiRateLimitExceededError) return true;
  const statusCode = (e as { statusCode?: number } | null)?.statusCode;
  if (typeof statusCode === "number" && (statusCode >= 500 || statusCode === 429)) return true;
  if (statusCode === undefined) {
    if (e instanceof FgaApiError) return true; // HTTP 응답 없음 = 네트워크 단계 오류.
    const code = (e as { code?: string } | null)?.code;
    if (code && TRANSIENT_CODES.has(code)) return true;
    const m = String((e as { message?: string } | null)?.message ?? e).toLowerCase();
    if (
      m.includes("fetch failed") ||
      m.includes("network") ||
      m.includes("timeout") ||
      m.includes("econnrefused")
    )
      return true;
    // 정체불명 + status 없음 → 무한재시도 방지 위해 결정적으로 취급(false).
  }
  return false;
}

/**
 * write/delete 오류 분류(lazyfga-15 hardening). transient면 502, 아니면 멱등 흡수는
 * invalid-input 코드 + op별 **정확한** 패턴이 함께 맞을 때만 한다:
 * - write 멱등 = 이미 존재하는 tuple(duplicate): "already exists" | "duplicate".
 * - delete 멱등 = 없는 tuple(missing): "cannot delete" | "does not exist".
 * delete 패턴에서 막연한 "not found"는 제외한다 — invalid-input + "relation/type not found" 같은
 * **실제** 거부를 missing no-op으로 삼켜 deleted:false로 숨기는 over-match를 막는다(LFGA-20 review).
 */
export function classifyWriteError(
  e: unknown,
  op: "write" | "delete",
): { idempotent: boolean; transient: boolean } {
  if (isTransientApiError(e)) return { idempotent: false, transient: true };
  // 결정적(4xx 등): 멱등 흡수는 invalid-input 코드/신호 + op별 정확 패턴이 함께 맞을 때만.
  const apiCode = (e as { responseData?: { code?: string } } | null)?.responseData?.code;
  const msg = String((e as { message?: string } | null)?.message ?? e).toLowerCase();
  const isInvalidInput =
    apiCode === "write_failed_due_to_invalid_input" ||
    msg.includes("write_failed_due_to_invalid_input");
  const opPattern =
    op === "write"
      ? msg.includes("already exists") || msg.includes("duplicate")
      : msg.includes("cannot delete") || msg.includes("does not exist");
  return { idempotent: isInvalidInput && opPattern, transient: false };
}
