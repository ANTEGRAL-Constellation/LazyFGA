import type { EvaluationRequest, ReasonResult } from "@lazyfga/shared";
import { useState } from "react";
import { useExplainStore, type Highlight } from "../../store/explainStore";

// vite dev proxy(/api → api 서버)를 거친다(같은 출처 호출 → CORS 회피).
const API_BASE = "/api";

function computeHighlight(req: EvaluationRequest, reason: ReasonResult): Highlight {
  // 경로가 없으면(거부 / reason 없음 / 재구성 실패) 강조하지 않는다.
  if (!reason.path || reason.path.length === 0) return { nodes: [], edges: [] };
  const nodes = new Set<string>([req.resource.type]);
  const edges: string[] = [];
  let prev = req.resource.type;
  for (const step of reason.path ?? []) {
    if (step.via === "role") {
      nodes.add(step.on);
      if (step.group) nodes.add(step.group);
    } else {
      nodes.add(step.parent);
      edges.push(`${prev}|${step.relation}|${step.parent}`); // buildEdges의 id 규칙과 동일
      prev = step.parent;
    }
  }
  return { nodes: [...nodes], edges };
}

export function useExplain(): {
  token: string;
  setToken(t: string): void;
  run(req: EvaluationRequest): Promise<void>;
  result?: ReasonResult;
  loading: boolean;
  error?: string;
} {
  const setHighlight = useExplainStore((s) => s.setHighlight);
  const [token, setToken] = useState("");
  const [result, setResult] = useState<ReasonResult | undefined>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | undefined>();

  async function run(req: EvaluationRequest): Promise<void> {
    setLoading(true);
    setError(undefined);
    try {
      const res = await fetch(`${API_BASE}/access/v1/evaluation`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          ...(token ? { authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ ...req, options: { reason: true } }),
      });
      if (!res.ok) {
        setError(`evaluation failed: HTTP ${res.status}`);
        setResult(undefined);
        setHighlight({ nodes: [], edges: [] });
        return;
      }
      const data = (await res.json()) as { decision: boolean; context?: { reason?: ReasonResult } };
      const reason: ReasonResult =
        data.context?.reason ?? { decision: data.decision, text: data.decision ? "allowed" : "denied" };
      setResult(reason);
      setHighlight(computeHighlight(req, reason));
    } catch (e) {
      setError(String(e));
      setResult(undefined);
      setHighlight({ nodes: [], edges: [] });
    } finally {
      setLoading(false);
    }
  }

  return { token, setToken, run, result, loading, error };
}
