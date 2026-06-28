/**
 * 변경 감사 기록. MVP는 구조화 로그이며, lazyfga-17에서 DB(audit_log)로 교체된다.
 */
export function recordAudit(action: string, data: Record<string, unknown>): void {
  console.log(`[audit] ${action} ${JSON.stringify(data)}`);
}
