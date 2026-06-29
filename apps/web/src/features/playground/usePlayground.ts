import { useState } from "react";

// vite dev proxy(/api → api). 발행본 모델/정책 기준으로 평가한다(lazyfga-9).
const API_BASE = "/api";
const STORAGE_KEY = "lazyfga.playground.cases";

export interface TestCase {
  subject: { type: string; id: string };
  action: { name: string };
  resource: { type: string; id: string };
  context?: Record<string, unknown>;
  expected?: boolean;
}
export interface CaseResult {
  decision?: boolean;
  pass?: boolean;
  error?: string;
}

const DEFAULT_CASES: TestCase[] = [
  { subject: { type: "user", id: "alice" }, action: { name: "read" }, resource: { type: "document", id: "123" } },
];

function loadCases(): TestCase[] {
  try {
    const s = localStorage.getItem(STORAGE_KEY);
    if (!s) return DEFAULT_CASES;
    const parsed = JSON.parse(s) as unknown;
    return Array.isArray(parsed) ? (parsed as TestCase[]) : DEFAULT_CASES;
  } catch {
    return DEFAULT_CASES;
  }
}

export interface PolicyOptions {
  actions: string[];
  resourceTypes: string[];
}

export function usePlayground(): {
  token: string;
  setToken(t: string): void;
  cases: TestCase[];
  setCases(next: TestCase[]): void;
  results: CaseResult[];
  running: boolean;
  runAll(): Promise<void>;
  loadPolicyOptions(): Promise<void>;
  policyOptions: PolicyOptions;
} {
  const [token, setToken] = useState("");
  const [cases, setCasesState] = useState<TestCase[]>(loadCases);
  const [results, setResults] = useState<CaseResult[]>([]);
  const [running, setRunning] = useState(false);
  const [policyOptions, setPolicyOptions] = useState<PolicyOptions>({ actions: [], resourceTypes: [] });

  const setCases = (next: TestCase[]): void => {
    setCasesState(next);
    setResults([]); // 케이스가 바뀌면 이전 결과를 무효화(인덱스 정렬 어긋남 방지).
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
    } catch {
      /* ignore quota/availability */
    }
  };

  // 발행본 정렬 픽커: GET /policies(admin). service 토큰이면 403 → 자유 입력만(graceful).
  async function loadPolicyOptions(): Promise<void> {
    try {
      const res = await fetch(`${API_BASE}/policies`, {
        headers: token ? { authorization: `Bearer ${token}` } : {},
      });
      if (!res.ok) return;
      const data = (await res.json()) as { policies: Array<{ permission: string; resourceType: string }> };
      setPolicyOptions({
        actions: [...new Set(data.policies.map((p) => p.permission))],
        resourceTypes: [...new Set(data.policies.map((p) => p.resourceType))],
      });
    } catch {
      /* best-effort */
    }
  }

  async function runAll(): Promise<void> {
    setRunning(true);
    try {
      const out: CaseResult[] = [];
      for (const tc of cases) {
        try {
          const res = await fetch(`${API_BASE}/access/v1/evaluation`, {
            method: "POST",
            headers: { "content-type": "application/json", ...(token ? { authorization: `Bearer ${token}` } : {}) },
            body: JSON.stringify({ subject: tc.subject, action: tc.action, resource: tc.resource, context: tc.context }),
          });
          if (!res.ok) {
            out.push({ error: `HTTP ${res.status}` });
            continue;
          }
          const data = (await res.json()) as { decision: boolean };
          out.push({
            decision: data.decision,
            pass: tc.expected === undefined ? undefined : data.decision === tc.expected,
          });
        } catch (e) {
          out.push({ error: String(e) });
        }
      }
      setResults(out);
    } finally {
      setRunning(false);
    }
  }

  return { token, setToken, cases, setCases, results, running, runAll, loadPolicyOptions, policyOptions };
}
