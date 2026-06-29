// lazyfga-17: audit 엔트리 계약(web·api 공유).
export interface AuditEntry {
  id: string;
  /** ISO 8601 (삽입 시각, 이벤트 시각 근사). */
  occurredAt: string;
  /** "admin" | "service:<tokenId>" | "idp:<provider>" | "system" */
  actor: string;
  action: string;
  data: Record<string, unknown>;
}
