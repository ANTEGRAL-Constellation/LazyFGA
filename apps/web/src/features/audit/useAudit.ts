import type { AuditEntry } from "@lazyfga/shared";
import { useState } from "react";

// vite dev proxy(/api → api)를 거친다.
const API_BASE = "/api";

export function useAudit(): {
  token: string;
  setToken(t: string): void;
  action: string;
  setAction(a: string): void;
  actor: string;
  setActor(a: string): void;
  entries: AuditEntry[];
  hasMore: boolean;
  loading: boolean;
  error?: string;
  load(more?: boolean): Promise<void>;
} {
  const [token, setToken] = useState("");
  const [action, setActionState] = useState("");
  const [actor, setActorState] = useState("");
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [cursor, setCursor] = useState<string | undefined>();
  // 필터가 바뀌면 페이지네이션 커서를 리셋한다(낡은 커서로 'Load more' 방지).
  const setAction = (a: string): void => {
    setActionState(a);
    setCursor(undefined);
  };
  const setActor = (a: string): void => {
    setActorState(a);
    setCursor(undefined);
  };
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | undefined>();

  async function load(more = false): Promise<void> {
    setLoading(true);
    setError(undefined);
    try {
      const params = new URLSearchParams();
      if (action) params.set("action", action);
      if (actor) params.set("actor", actor);
      if (more && cursor) params.set("cursor", cursor);
      const res = await fetch(`${API_BASE}/audit?${params.toString()}`, {
        headers: token ? { authorization: `Bearer ${token}` } : {},
      });
      if (res.status === 401 || res.status === 403) {
        setError("admin token required");
        return;
      }
      if (!res.ok) {
        setError(`audit query failed: HTTP ${res.status}`);
        return;
      }
      const data = (await res.json()) as { entries: AuditEntry[]; nextCursor?: string };
      setEntries((prev) => (more ? [...prev, ...data.entries] : data.entries));
      setCursor(data.nextCursor);
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }

  return {
    token,
    setToken,
    action,
    setAction,
    actor,
    setActor,
    entries,
    hasMore: cursor !== undefined,
    loading,
    error,
    load,
  };
}
