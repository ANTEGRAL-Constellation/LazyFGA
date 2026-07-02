package democli

import (
	"context"
	"encoding/json"
	"io"

	"github.com/antegral-constellation/lazyfga/api/internal/modules/idp"
)

// seed-zitadel-rules CLI (TS apps/api/scripts/seed-zitadel-rules.ts 포팅). repo-level·idempotent.
// project 단위(추가/삭제 대칭) 매핑 규칙 2개를 clear-then-insert로 시드한다.
// 전제: team#member를 가진 모델이 먼저 발행돼 있어야 grant write가 성공한다(Run이 담당).

// RuleSeedRepo는 seed가 필요로 하는 idp 저장소 연산이다(*idp.DBRepo가 만족).
type RuleSeedRepo interface {
	GetConnectionByProvider(ctx context.Context, provider string) (*idp.Connection, error)
	CreateConnection(ctx context.Context, in idp.CreateConnectionInput) (*idp.PublicConnection, error)
	ListRulesByConnection(ctx context.Context, connID string) ([]idp.StoredRule, error)
	DeleteRule(ctx context.Context, ruleID string) (bool, error)
	CreateRule(ctx context.Context, connID string, in idp.CreateRuleInput) (*idp.StoredRule, error)
}

// SeedDeps는 seed-zitadel-rules의 의존성이다.
type SeedDeps struct {
	Repo          RuleSeedRepo
	SigningSecret string
	Out           io.Writer
}

// teamMembership은 project grant → OpenFGA team 멤버십 매핑이다. attributes.project로 키잉(삭제 신뢰).
// 데모 모델엔 project 타입이 없으므로 ZITADEL project → OpenFGA team 그룹 멤버십으로 매핑한다.
var teamMembership = idp.TupleTemplate{
	User:     "user:{{subject}}",
	Relation: "member",
	Object:   "team:{{attributes.project}}",
}

// SeedZitadelRules는 zitadel 연결을 보장하고 project 기반 규칙 2개(added→write, removed→delete)를 시드한다.
func SeedZitadelRules(ctx context.Context, deps SeedDeps) error {
	out := deps.Out
	if out == nil {
		out = io.Discard
	}

	existing, err := deps.Repo.GetConnectionByProvider(ctx, "zitadel")
	if err != nil {
		return err
	}
	var connID string
	if existing != nil {
		connID = existing.ID
		emitf(out, "zitadel connection exists\n")
	} else {
		preset := "zitadel"
		conn, err := deps.Repo.CreateConnection(ctx, idp.CreateConnectionInput{
			Provider:      "zitadel",
			Preset:        &preset,
			SigningSecret: deps.SigningSecret,
			Enabled:       true,
		})
		if err != nil {
			return err
		}
		connID = conn.ID
		emitf(out, "created zitadel connection\n")
	}

	// clear-then-insert로 멱등화(규칙 테이블엔 unique 키가 없음).
	rules, err := deps.Repo.ListRulesByConnection(ctx, connID)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if _, err := deps.Repo.DeleteRule(ctx, r.ID); err != nil {
			return err
		}
	}
	for _, spec := range []struct{ eventType, op string }{
		{"user.grant.added", "write"},
		{"user.grant.removed", "delete"},
	} {
		if _, err := deps.Repo.CreateRule(ctx, connID, idp.CreateRuleInput{
			EventType:     spec.eventType,
			Match:         json.RawMessage("[]"),
			TupleTemplate: teamMembership,
			Op:            spec.op,
		}); err != nil {
			return err
		}
	}
	emitf(out, "seeded 2 zitadel mapping rules (project-based: added → write, removed → delete)\n")
	return nil
}
