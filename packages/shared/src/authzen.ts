// OpenID AuthZEN 1.0 Access Evaluation 요청/응답(lazyfga-9).

export interface EvaluationRequest {
  subject: { type: string; id: string };
  action: { name: string };
  resource: { type: string; id: string };
  context?: Record<string, unknown>;
  /** reason=true면 응답 context.reason에 설명 부착(lazyfga-11). */
  options?: { reason?: boolean };
}

export interface EvaluationResponse {
  decision: boolean;
  context?: Record<string, unknown>;
}
