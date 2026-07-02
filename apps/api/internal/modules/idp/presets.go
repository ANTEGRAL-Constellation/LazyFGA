package idp

// in-repo preset 레지스트리(설정, 로직 아님). provider 추가 = preset 작성.
// connection.preset 키로 해석된다(webhook 핸들러). ZITADEL은 실제 소스로 검증된 spec.

// zitadelPreset — ZITADEL (Actions V2). 서명: pkg/actions/signing.go — 헤더 `ZITADEL-Signature`,
// `t=<unixSeconds>,v1=<hex>[,v1=...]`(키회전 다중), HMAC-SHA256("<sec>.<body>") hex, tolerance 300s.
// 추출: signup은 user-aggregate(aggregateID=신규 user), grant는 usergrant-aggregate
// (event_payload.userId=사용자). removed엔 roleKeys 없음 → project 단위 대칭.
var zitadelPreset = ProviderPreset{
	Signature: WebhookSignatureSpec{
		Header:                  "ZITADEL-Signature",
		HeaderFormat:            "kv_t_v",
		TimestampSource:         "signature_header",
		TimestampUnit:           "seconds",
		PayloadTemplate:         "{timestamp}.{body}",
		Algorithm:               "sha256",
		Encoding:                "hex",
		SecretEncoding:          "raw",
		ToleranceSec:            300,
		AllowMultipleSignatures: true,
	},
	TypePath: "event_type",
	Extraction: []EventExtractionRule{
		{
			Match:          []string{"user.human.added", "user.human.selfregistered"},
			SubjectType:    "user",
			SubjectIDPath:  "aggregateID",
			AttributePaths: []AttributePath{{Name: "org", Path: "resourceOwner"}},
		},
		{
			Match:         []string{"user.grant.added"},
			SubjectType:   "user",
			SubjectIDPath: "event_payload.userId",
			AttributePaths: []AttributePath{
				{Name: "project", Path: "event_payload.projectId"},
				{Name: "roleKeys", Path: "event_payload.roleKeys"},
			},
		},
		{
			Match:          []string{"user.grant.removed"},
			SubjectType:    "user",
			SubjectIDPath:  "event_payload.userId",
			AttributePaths: []AttributePath{{Name: "project", Path: "event_payload.projectId"}},
		},
	},
}

// standardWebhooksPreset — Standard Webhooks (https://www.standardwebhooks.com/). 범용성 증명용.
// 서명: 헤더 `webhook-signature` = `v1,<base64>`(공백 구분 다중), 서명대상 `{id}.{timestamp}.{body}`,
// id=`webhook-id`, timestamp=`webhook-timestamp`(초), HMAC-SHA256 base64, secret=`whsec_<base64>`.
var standardWebhooksPreset = ProviderPreset{
	Signature: WebhookSignatureSpec{
		Header:                  "webhook-signature",
		HeaderFormat:            "standard_webhooks",
		TimestampSource:         "separate_header",
		TimestampHeader:         "webhook-timestamp",
		TimestampUnit:           "seconds",
		IDSource:                &IDSource{Header: "webhook-id"},
		PayloadTemplate:         "{id}.{timestamp}.{body}",
		Algorithm:               "sha256",
		Encoding:                "base64",
		SecretEncoding:          "base64",
		SecretPrefix:            "whsec_",
		ToleranceSec:            300,
		AllowMultipleSignatures: true,
	},
	TypePath: "type",
	Extraction: []EventExtractionRule{
		{
			Match:          []string{"user.created", "user.updated"},
			SubjectType:    "user",
			SubjectIDPath:  "data.id",
			AttributePaths: []AttributePath{{Name: "org", Path: "data.orgId"}},
		},
	},
}

// presetKeys는 미지정 preset 오류 메시지의 "known" 목록 순서를 고정한다
// (TS Object.keys(PRESETS) 순서: zitadel 먼저).
var presetKeys = []string{"zitadel", "standard-webhooks"}

// presetByKey는 키로 preset을 해석한다. 미지정 키면 ok=false.
func presetByKey(key string) (ProviderPreset, bool) {
	switch key {
	case "zitadel":
		return zitadelPreset, true
	case "standard-webhooks":
		return standardWebhooksPreset, true
	}
	return ProviderPreset{}, false
}

// presetKnown은 키가 알려진 preset인지 보고한다.
func presetKnown(key string) bool {
	_, ok := presetByKey(key)
	return ok
}
