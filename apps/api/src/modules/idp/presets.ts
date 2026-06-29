import type { ProviderPreset } from "./extraction";

// lazyfga-21: in-repo preset 레지스트리(설정, 로직 아님). provider 추가 = preset 작성.
// connection.preset 키로 해석된다(webhook 핸들러). ZITADEL은 실제 소스로 검증된 spec.

/**
 * ZITADEL (Actions V2). 서명: pkg/actions/signing.go — 헤더 `ZITADEL-Signature`,
 * `t=<unixSeconds>,v1=<hex>[,v1=...]`(키회전 다중), HMAC-SHA256("<sec>.<body>") hex, tolerance 300s.
 * 이벤트: ContextInfoEvent — top-level `event_type`/`aggregateID`/`resourceOwner` + `event_payload`.
 * 추출 규칙은 이벤트 패밀리별 aggregate 차이를 반영한다(소스 대조):
 *  - signup(user.human.added/selfregistered): user-aggregate → subject = top-level `aggregateID`.
 *  - grant(user.grant.added/removed): usergrant-aggregate → subject = `event_payload.userId`
 *    (aggregateID는 grant id이지 사용자가 아님). removed엔 roleKeys 없음 → project 단위 대칭.
 */
const zitadel: ProviderPreset = {
  signature: {
    header: "ZITADEL-Signature",
    headerFormat: "kv_t_v",
    timestampSource: "signature_header",
    timestampUnit: "seconds",
    payloadTemplate: "{timestamp}.{body}",
    algorithm: "sha256",
    encoding: "hex",
    secretEncoding: "raw",
    toleranceSec: 300,
    allowMultipleSignatures: true,
  },
  typePath: "event_type",
  extraction: [
    {
      // 회원가입 → 소속 org 멤버십(기본 관계). user-aggregate → aggregateID가 신규 user id.
      match: ["user.human.added", "user.human.selfregistered"],
      subjectType: "user",
      subjectIdPath: "aggregateID",
      attributePaths: { org: "resourceOwner" },
    },
    {
      // 프로젝트 grant 추가. usergrant-aggregate → subject는 event_payload.userId.
      // roleKeys는 (added에만 존재) 배열 attribute로 노출 — 선택적 fan-out 데모용.
      match: ["user.grant.added"],
      subjectType: "user",
      subjectIdPath: "event_payload.userId",
      attributePaths: { project: "event_payload.projectId", roleKeys: "event_payload.roleKeys" },
    },
    {
      // 프로젝트 grant 제거. UserGrantRemovedEvent엔 roleKeys 없음 → project 단위 대칭(삭제).
      match: ["user.grant.removed"],
      subjectType: "user",
      subjectIdPath: "event_payload.userId",
      attributePaths: { project: "event_payload.projectId" },
    },
  ],
};

/**
 * Standard Webhooks (https://www.standardwebhooks.com/) — 범용성 증명용 2번째 preset.
 * 서명: 헤더 `webhook-signature` = `v1,<base64>`(공백 구분 다중), 서명대상 `{id}.{timestamp}.{body}`,
 * id=`webhook-id`, timestamp=`webhook-timestamp`(초), HMAC-SHA256 base64, secret=`whsec_<base64>`.
 * payload 스키마는 표준이 강제하지 않으므로 아래 추출은 합리적 예시(연결별 override 가능).
 */
const standardWebhooks: ProviderPreset = {
  signature: {
    header: "webhook-signature",
    headerFormat: "standard_webhooks",
    timestampSource: "separate_header",
    timestampHeader: "webhook-timestamp",
    timestampUnit: "seconds",
    idSource: { header: "webhook-id" },
    payloadTemplate: "{id}.{timestamp}.{body}",
    algorithm: "sha256",
    encoding: "base64",
    secretEncoding: "base64",
    secretPrefix: "whsec_",
    toleranceSec: 300,
    allowMultipleSignatures: true,
  },
  typePath: "type",
  extraction: [
    {
      match: ["user.created", "user.updated"],
      subjectType: "user",
      subjectIdPath: "data.id",
      attributePaths: { org: "data.orgId" },
    },
  ],
};

export const PRESETS: Record<string, ProviderPreset> = {
  zitadel,
  "standard-webhooks": standardWebhooks,
};
