import { db } from "../../db/client";
import { auditLog } from "../../db/schema";
import type { Principal } from "../../middleware/auth";

/**
 * 변경 감사 기록(lazyfga-17). DB(audit_log)에 비차단으로 적재한다.
 * **감사 실패가 감사 대상 작업을 절대 깨지 않는다**(fire-and-forget + 내부 catch).
 * 시그니처는 하위호환: 기존 `recordAudit(action, data)` 호출은 그대로 동작(actor 기본 "system").
 */
export function recordAudit(action: string, data?: Record<string, unknown>, actor?: string): void {
  try {
    void db
      .insert(auditLog)
      .values({ action, data: data ?? {}, actor: actor ?? "system" })
      .catch((e: unknown) => console.error(`[audit] insert failed: ${action}`, e));
  } catch (e) {
    // 쿼리 빌드 단계의 동기 throw까지 흡수(감사가 절대 호출자를 깨지 않는다는 보장 유지).
    console.error(`[audit] insert threw synchronously: ${action}`, e);
  }
}

/** principal → audit actor 문자열. */
export function principalActor(p: Principal): string {
  if (p.role === "admin") return "admin";
  return p.tokenId ? `service:${p.tokenId}` : "service";
}
