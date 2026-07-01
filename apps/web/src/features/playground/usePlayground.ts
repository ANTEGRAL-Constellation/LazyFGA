import { useRef, useState } from "react";

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
  {
    subject: { type: "user", id: "alice" },
    action: { name: "read" },
    resource: { type: "document", id: "123" },
  },
];

function isTestCase(c: unknown): c is TestCase {
  if (typeof c !== "object" || c === null) return false;
  const o = c as Record<string, unknown>;
  const subj = o.subject as Record<string, unknown> | undefined;
  const act = o.action as Record<string, unknown> | undefined;
  const res = o.resource as Record<string, unknown> | undefined;
  return (
    !!subj &&
    typeof subj.type === "string" &&
    typeof subj.id === "string" &&
    !!act &&
    typeof act.name === "string" &&
    !!res &&
    typeof res.type === "string" &&
    typeof res.id === "string"
  );
}

function loadCases(): TestCase[] {
  try {
    const s = localStorage.getItem(STORAGE_KEY);
    if (!s) return DEFAULT_CASES;
    const parsed = JSON.parse(s) as unknown;
    // 배열 + 모든 원소가 TestCase 형태일 때만 채택(예: `[{}]` 같은 손상값 → 렌더 크래시 방지).
    return Array.isArray(parsed) && parsed.every(isTestCase) ? parsed : DEFAULT_CASES;
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
  const [policyOptions, setPolicyOptions] = useState<PolicyOptions>({
    actions: [],
    resourceTypes: [],
  });
  // 케이스 세대 번호. 진행 중 runAll의 결과를 그 사이 변경된 케이스에 잘못 매핑하지 않도록 무효화.
  const generation = useRef(0);

  const setCases = (next: TestCase[]): void => {
    generation.current += 1;
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
      const data = (await res.json()) as {
        policies: Array<{ permission: string; resourceType: string }>;
      };
      setPolicyOptions({
        actions: [...new Set(data.policies.map((p) => p.permission))],
        resourceTypes: [...new Set(data.policies.map((p) => p.resourceType))],
      });
    } catch {
      /* best-effort */
    }
  }

  async function runAll(): Promise<void> {
    const gen = generation.current;
    setRunning(true);
    try {
      const out: CaseResult[] = [];
      for (const tc of cases) {
        try {
          const res = await fetch(`${API_BASE}/access/v1/evaluation`, {
            method: "POST",
            headers: {
              "content-type": "application/json",
              ...(token ? { authorization: `Bearer ${token}` } : {}),
            },
            body: JSON.stringify({
              subject: tc.subject,
              action: tc.action,
              resource: tc.resource,
              context: tc.context,
            }),
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
      // 실행 도중 케이스가 바뀌었으면(세대 변경) 낡은 결과를 버린다(index drift 방지).
      if (generation.current === gen) setResults(out);
    } finally {
      setRunning(false);
    }
  }

  return {
    token,
    setToken,
    cases,
    setCases,
    results,
    running,
    runAll,
    loadPolicyOptions,
    policyOptions,
  };
}
