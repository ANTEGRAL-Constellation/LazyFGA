package idp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antegral-constellation/lazyfga/api/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── 통합 테스트(scratch DB) ──

func requireIntegration(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		if os.Getenv("LAZYFGA_TEST_INTEGRATION") == "1" {
			t.Fatal("LAZYFGA_TEST_INTEGRATION=1 but DATABASE_URL is unset")
		}
		t.Skip("DATABASE_URL unset; skipping DB integration test")
	}
	return raw
}

func urlWithDBName(raw, dbName string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic("invalid DATABASE_URL: " + err.Error())
	}
	u.Path = "/" + dbName
	return u.String()
}

func newMigratedScratchDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := requireIntegration(t)
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	scratchName := "lazyfga_test_" + hex.EncodeToString(b)
	if !strings.HasPrefix(scratchName, "lazyfga_test_") {
		t.Fatalf("unsafe scratch name %q", scratchName)
	}
	adminURL := urlWithDBName(raw, "postgres")
	scratchURL := urlWithDBName(raw, scratchName)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, scratchName)); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create scratch: %v", err)
	}
	_ = admin.Close(ctx)

	pool, err := db.Connect(context.Background(), scratchURL)
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	if err := db.NewMigrator().Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate scratch: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dctx, dcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dcancel()
		a, err := pgx.Connect(dctx, adminURL)
		if err != nil {
			return
		}
		defer func() { _ = a.Close(dctx) }()
		_, _ = a.Exec(dctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, scratchName)
		_, _ = a.Exec(dctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q`, scratchName))
	})
	return pool
}

// rawMsgPtr는 raw JSON 패치 포인터 헬퍼다.
func rawMsgPtr(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

func TestRepo_Connections(t *testing.T) {
	pool := newMigratedScratchDB(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	// create (with preset + secret).
	created, err := repo.CreateConnection(ctx, CreateConnectionInput{
		Provider: "zitadel", Preset: strp("zitadel"), SigningSecret: "sekret", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	if created.ID == "" || created.Provider != "zitadel" || created.Preset == nil || *created.Preset != "zitadel" || !created.Enabled {
		t.Fatalf("created = %+v", created)
	}

	// duplicate provider → ErrDuplicateProvider (409).
	if _, err := repo.CreateConnection(ctx, CreateConnectionInput{Provider: "zitadel", SigningSecret: "x", Enabled: true}); err != ErrDuplicateProvider {
		t.Fatalf("duplicate err = %v, want ErrDuplicateProvider", err)
	}

	// create a second (no preset → null).
	if _, err := repo.CreateConnection(ctx, CreateConnectionInput{Provider: "acme", Preset: nil, SigningSecret: "s2", Enabled: false}); err != nil {
		t.Fatalf("CreateConnection acme: %v", err)
	}

	// list ordered by provider asc.
	list, err := repo.ListConnections(ctx)
	if err != nil {
		t.Fatalf("ListConnections: %v", err)
	}
	if len(list) != 2 || list[0].Provider != "acme" || list[1].Provider != "zitadel" {
		t.Fatalf("list = %+v", list)
	}

	// getByID (no secret exposed via PublicConnection type).
	got, err := repo.GetConnectionByID(ctx, created.ID)
	if err != nil || got == nil || got.Provider != "zitadel" {
		t.Fatalf("GetConnectionByID = %+v, %v", got, err)
	}
	if missing, _ := repo.GetConnectionByID(ctx, "00000000-0000-0000-0000-000000000000"); missing != nil {
		t.Error("unknown id must return nil")
	}

	// getByProvider (with secret) for webhook verification.
	full, err := repo.GetConnectionByProvider(ctx, "zitadel")
	if err != nil || full == nil || full.SigningSecret != "sekret" {
		t.Fatalf("GetConnectionByProvider = %+v, %v", full, err)
	}
	if miss, _ := repo.GetConnectionByProvider(ctx, "nope"); miss != nil {
		t.Error("unknown provider must return nil")
	}

	// update: secret + enabled + preset.
	updated, err := repo.UpdateConnection(ctx, created.ID, ConnectionPatch{
		SigningSecret: strp("newsecret"), Enabled: boolp(false), Preset: strp("standard-webhooks"),
	})
	if err != nil || updated == nil || updated.Enabled || updated.Preset == nil || *updated.Preset != "standard-webhooks" {
		t.Fatalf("UpdateConnection = %+v, %v", updated, err)
	}
	full2, _ := repo.GetConnectionByProvider(ctx, "zitadel")
	if full2.SigningSecret != "newsecret" {
		t.Errorf("secret not updated: %q", full2.SigningSecret)
	}
	// update unknown → nil.
	if u, _ := repo.UpdateConnection(ctx, "00000000-0000-0000-0000-000000000000", ConnectionPatch{Enabled: boolp(true)}); u != nil {
		t.Error("update unknown must return nil")
	}

	// delete + cascade check.
	ok, err := repo.DeleteConnection(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteConnection = %v, %v", ok, err)
	}
	if again, _ := repo.DeleteConnection(ctx, created.ID); again {
		t.Error("re-delete must be false")
	}
}

func TestRepo_Rules(t *testing.T) {
	pool := newMigratedScratchDB(t)
	repo := NewRepo(pool)
	ctx := context.Background()

	conn, err := repo.CreateConnection(ctx, CreateConnectionInput{Provider: "zitadel", Preset: strp("zitadel"), SigningSecret: "s", Enabled: true})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	// create rule with match [] default and no fanOut.
	r1, err := repo.CreateRule(ctx, conn.ID, CreateRuleInput{
		EventType:     "user.grant.added",
		Match:         json.RawMessage("[]"),
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"},
		Op:            "write",
		Priority:      2,
	})
	if err != nil {
		t.Fatalf("CreateRule r1: %v", err)
	}
	if r1.FanOut != nil || r1.Op != "write" || r1.Priority != 2 || string(r1.Match) != "[]" {
		t.Fatalf("r1 = %+v", r1)
	}

	// create fan-out rule with match predicates.
	r2, err := repo.CreateRule(ctx, conn.ID, CreateRuleInput{
		EventType:     "user.grant.added",
		Match:         json.RawMessage(`[{"field":"subject","equals":"alice"}]`),
		TupleTemplate: TupleTemplate{User: "user:{{subject}}", Relation: "{{item}}", Object: "team:{{attributes.project}}"},
		Op:            "delete",
		FanOut:        strp("roleKeys"),
		Priority:      1,
	})
	if err != nil {
		t.Fatalf("CreateRule r2: %v", err)
	}
	if r2.FanOut == nil || *r2.FanOut != "roleKeys" || r2.Op != "delete" {
		t.Fatalf("r2 = %+v", r2)
	}

	// list by connection ordered by priority asc → r2(1), r1(2).
	rules, err := repo.ListRulesByConnection(ctx, conn.ID)
	if err != nil || len(rules) != 2 || rules[0].ID != r2.ID || rules[1].ID != r1.ID {
		t.Fatalf("ListRulesByConnection = %+v, %v", rules, err)
	}

	// getByID.
	got, err := repo.GetRuleByID(ctx, r1.ID)
	if err != nil || got == nil || got.EventType != "user.grant.added" {
		t.Fatalf("GetRuleByID = %+v, %v", got, err)
	}
	if miss, _ := repo.GetRuleByID(ctx, "00000000-0000-0000-0000-000000000000"); miss != nil {
		t.Error("unknown rule must be nil")
	}

	// getRulesByProvider (join) ordered by priority asc.
	byProv, err := repo.GetRulesByProvider(ctx, "zitadel")
	if err != nil || len(byProv) != 2 || byProv[0].Op != "delete" || byProv[1].Op != "write" {
		t.Fatalf("GetRulesByProvider = %+v, %v", byProv, err)
	}
	if byProv[0].FanOut == nil || *byProv[0].FanOut != "roleKeys" {
		t.Errorf("fanOut not loaded: %+v", byProv[0])
	}

	// update rule: change eventType, match, tupleTemplate, op, priority, clear fanOut.
	upd, err := repo.UpdateRule(ctx, r2.ID, RulePatch{
		EventType:     strp("user.grant.removed"),
		Match:         rawMsgPtr(`[{"field":"type","equals":"user.grant.removed"}]`),
		TupleTemplate: &TupleTemplate{User: "user:{{subject}}", Relation: "member", Object: "team:{{attributes.project}}"},
		Op:            strp("write"),
		Priority:      intp(9),
		FanOutSet:     true,
		FanOutValue:   nil, // clear
	})
	if err != nil || upd == nil {
		t.Fatalf("UpdateRule: %+v, %v", upd, err)
	}
	if upd.EventType != "user.grant.removed" || upd.Op != "write" || upd.Priority != 9 || upd.FanOut != nil {
		t.Fatalf("updated = %+v", upd)
	}
	// jsonb 정규화 키 순서(field(5) < equals(6)) + compact 형태로 에코된다(TS parity).
	if string(upd.Match) != `[{"field":"type","equals":"user.grant.removed"}]` {
		t.Fatalf("updated match = %s", upd.Match)
	}

	// update rule: set fanOut back to a value.
	upd2, err := repo.UpdateRule(ctx, r2.ID, RulePatch{FanOutSet: true, FanOutValue: strp("roleKeys")})
	if err != nil || upd2.FanOut == nil || *upd2.FanOut != "roleKeys" {
		t.Fatalf("UpdateRule set fanOut = %+v, %v", upd2, err)
	}
	// update unknown → nil.
	if u, _ := repo.UpdateRule(ctx, "00000000-0000-0000-0000-000000000000", RulePatch{Priority: intp(1)}); u != nil {
		t.Error("update unknown rule must be nil")
	}

	// delete cascade: deleting the connection removes its rules.
	if _, err := repo.DeleteConnection(ctx, conn.ID); err != nil {
		t.Fatalf("DeleteConnection: %v", err)
	}
	remaining, _ := repo.ListRulesByConnection(ctx, conn.ID)
	if len(remaining) != 0 {
		t.Errorf("rules must cascade on connection delete, got %d", len(remaining))
	}
}

func TestRepo_DeleteRule(t *testing.T) {
	pool := newMigratedScratchDB(t)
	repo := NewRepo(pool)
	ctx := context.Background()
	conn, _ := repo.CreateConnection(ctx, CreateConnectionInput{Provider: "p", SigningSecret: "s", Enabled: true})
	rule, _ := repo.CreateRule(ctx, conn.ID, CreateRuleInput{
		EventType: "e", Match: json.RawMessage("[]"), TupleTemplate: TupleTemplate{User: "u:1", Relation: "r", Object: "o:1"}, Op: "write",
	})
	ok, err := repo.DeleteRule(ctx, rule.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteRule = %v, %v", ok, err)
	}
	if again, _ := repo.DeleteRule(ctx, rule.ID); again {
		t.Error("re-delete must be false")
	}
}

func boolp(b bool) *bool { return &b }
func intp(i int) *int    { return &i }
