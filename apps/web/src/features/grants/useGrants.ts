import type { GrantEntry, GrantRequest, GrantSubject } from "@lazyfga/shared";
import { useState } from "react";

// vite dev proxy(/api → api)를 거친다. grant/revoke/list 전부 admin 토큰 필요.
const API_BASE = "/api";

export interface GrantForm {
  subjectType: string;
  subjectId: string;
  subjectRelation: string; // userset일 때만(예: "member"). 비면 concrete user/subject.
  relation: string;
  resourceType: string;
  resourceId: string;
  conditionName: string; // 선택(lazyfga-14). 비면 무조건.
}

const EMPTY: GrantForm = {
  subjectType: "user",
  subjectId: "",
  subjectRelation: "",
  relation: "",
  resourceType: "",
  resourceId: "",
  conditionName: "",
};

type RevokeBody = {
  subject: GrantSubject;
  relation: string;
  resource: { type: string; id: string };
};

function toSubject(f: GrantForm): GrantSubject {
  const base = { type: f.subjectType.trim(), id: f.subjectId.trim() };
  return f.subjectRelation.trim() ? { ...base, relation: f.subjectRelation.trim() } : base;
}

function toRequest(f: GrantForm): GrantRequest {
  const req: GrantRequest = {
    subject: toSubject(f),
    relation: f.relation.trim(),
    resource: { type: f.resourceType.trim(), id: f.resourceId.trim() },
  };
  if (f.conditionName.trim()) req.condition = { name: f.conditionName.trim() };
  return req;
}

function subjectString(s: GrantSubject): string {
  return s.relation ? `${s.type}:${s.id}#${s.relation}` : `${s.type}:${s.id}`;
}

export function useGrants(): {
  token: string;
  setToken(t: string): void;
  form: GrantForm;
  setForm(f: GrantForm): void;
  busy: boolean;
  status?: string;
  error?: string;
  entries: GrantEntry[];
  grant(): Promise<void>;
  revokeForm(): Promise<void>;
  revokeEntry(e: GrantEntry): Promise<void>;
  listByResource(): Promise<void>;
  listBySubject(): Promise<void>;
} {
  const [token, setToken] = useState("");
  const [form, setForm] = useState<GrantForm>(EMPTY);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<string | undefined>();
  const [error, setError] = useState<string | undefined>();
  const [entries, setEntries] = useState<GrantEntry[]>([]);
  // 마지막으로 실행한 list 쿼리(변경 뮤테이션 후 동일 쿼리로 갱신).
  const [lastQuery, setLastQuery] = useState<string | undefined>();

  const authHeaders = (): Record<string, string> => ({
    "content-type": "application/json",
    ...(token ? { authorization: `Bearer ${token}` } : {}),
  });

  // 실제 fetch + 상태 갱신(busy는 호출부가 관리 — mutate 후 갱신 시 깜빡임 방지).
  // 실패 시 entries/lastQuery를 비워 낡은 목록 표시 + 잘못된 쿼리로의 자동 갱신을 막는다(LFGA-20 review).
  async function fetchListInto(query: string): Promise<void> {
    setError(undefined);
    try {
      const res = await fetch(`${API_BASE}/grants?${query}`, { headers: authHeaders() });
      const data = (await res.json().catch(() => ({}))) as {
        grants?: GrantEntry[];
        error?: string;
        code?: string;
      };
      if (!res.ok) {
        setError(`${data.code ?? "error"}: ${data.error ?? `HTTP ${res.status}`}`);
        setEntries([]);
        setLastQuery(undefined);
        return;
      }
      setEntries(data.grants ?? []);
      setLastQuery(query);
    } catch (e) {
      setError(String(e));
      setEntries([]);
      setLastQuery(undefined);
    }
  }

  async function runList(query: string): Promise<void> {
    setBusy(true);
    await fetchListInto(query);
    setBusy(false);
  }

  async function mutate(method: "POST" | "DELETE", body: GrantRequest | RevokeBody): Promise<void> {
    setBusy(true);
    setStatus(undefined);
    setError(undefined);
    let ok = false;
    try {
      const res = await fetch(`${API_BASE}/grants`, {
        method,
        headers: authHeaders(),
        body: JSON.stringify(body),
      });
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      if (!res.ok) {
        setError(
          `${(data.code as string) ?? "error"}: ${(data.error as string) ?? `HTTP ${res.status}`}`,
        );
      } else {
        ok = true;
        if (method === "POST")
          setStatus(data.created ? "granted (new)" : "already granted (no-op)");
        else setStatus(data.deleted ? "revoked" : "already absent (no-op)");
      }
    } catch (e) {
      setError(String(e));
    }
    // 리스트가 떠 있으면 변경 후 같은 쿼리로 갱신(busy를 끄지 않고 이어서 — 깜빡임 없음).
    if (ok && lastQuery) await fetchListInto(lastQuery);
    setBusy(false);
  }

  return {
    token,
    setToken,
    form,
    setForm,
    busy,
    status,
    error,
    entries,
    grant: () => mutate("POST", toRequest(form)),
    revokeForm: () => {
      const r = toRequest(form);
      return mutate("DELETE", { subject: r.subject, relation: r.relation, resource: r.resource });
    },
    revokeEntry: (e) =>
      mutate("DELETE", { subject: e.subject, relation: e.relation, resource: e.resource }),
    listByResource: async () => {
      const t = form.resourceType.trim();
      const id = form.resourceId.trim();
      if (!t || !id) {
        setError("resource type and id required to list by resource");
        return;
      }
      await runList(`resource=${encodeURIComponent(`${t}:${id}`)}`);
    },
    listBySubject: async () => {
      const subj = toSubject(form);
      if (!subj.type || !subj.id) {
        setError("subject type and id required to list by subject");
        return;
      }
      const rt = form.resourceType.trim();
      const q = `subject=${encodeURIComponent(subjectString(subj))}${rt ? `&resourceType=${encodeURIComponent(rt)}` : ""}`;
      await runList(q);
    },
  };
}
