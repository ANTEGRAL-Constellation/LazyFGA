import type { EvaluationRequest, EvaluationResponse } from "@lazyfga/shared";
import { gateway } from "../../openfga";
import { recordAudit } from "../audit/audit";
import { getCurrentVersion } from "../model/model.repo";
import { findByActionResource } from "../policy/policy.repo";
import { explain } from "./reason";

export class EvaluateError extends Error {
  constructor(
    public readonly status: 500,
    public readonly detail: unknown,
  ) {
    super("evaluate failed");
    this.name = "EvaluateError";
  }
}

/**
 * 단일 질문 템플릿 평가(lazyfga-9): (action, resource.type)로 정책을 찾아 OpenFGA Check 1회.
 * 정책/모델 부재 → deny-by-default(200). OpenFGA 자체 오류(모델 깨짐) → EvaluateError(500).
 */
export async function evaluate(req: EvaluationRequest): Promise<EvaluationResponse> {
  const current = await getCurrentVersion();
  if (!current) return { decision: false, context: { reason_code: "MODEL_NOT_PUBLISHED" } };

  const policy = await findByActionResource(req.action.name, req.resource.type);
  if (!policy) return { decision: false, context: { reason_code: "NO_POLICY" } };

  const user = `${req.subject.type}:${req.subject.id}`;
  const object = `${req.resource.type}:${req.resource.id}`;
  const relation = `can_${policy.permission}`;

  let allowed: boolean;
  try {
    ({ allowed } = await gateway.check(
      { user, relation, object, context: req.context },
      { authorizationModelId: current.authorizationModelId },
    ));
  } catch (e) {
    // 모델-정책 불일치 등 OpenFGA 오류는 fail-closed가 아니라 500으로 표면화(무결성 이슈).
    recordAudit("pdp.evaluate.openfga_error", { user, relation, object, error: String(e) });
    throw new EvaluateError(500, String(e));
  }

  if (!req.options?.reason) return { decision: allowed };

  // reason은 요청 시에만(hot-path 경량 유지). decision/reason이 같은 모델 버전을 쓰도록 핀 전달.
  // reason 재구성 실패는 decision을 무효화하지 않는다(decision은 그대로 반환).
  try {
    const reason = await explain(user, policy.permission, object, req.context, {
      decision: allowed,
      authorizationModelId: current.authorizationModelId,
      ir: current.irJson,
    });
    return { decision: allowed, context: { reason } };
  } catch (e) {
    recordAudit("pdp.reason.error", { user, relation, object, error: String(e) });
    return { decision: allowed };
  }
}
