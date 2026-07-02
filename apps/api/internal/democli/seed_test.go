package democli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/antegral-constellation/lazyfga/api/internal/modules/idp"
)

type fakeSeedRepo struct {
	conn         *idp.Connection
	created      *idp.PublicConnection
	rules        []idp.StoredRule
	createdRules []idp.CreateRuleInput
	deletedRules []string

	getErr        error
	createConnErr error
	listErr       error
	deleteErr     error
	createRuleErr error
}

func (f *fakeSeedRepo) GetConnectionByProvider(_ context.Context, _ string) (*idp.Connection, error) {
	return f.conn, f.getErr
}

func (f *fakeSeedRepo) CreateConnection(_ context.Context, _ idp.CreateConnectionInput) (*idp.PublicConnection, error) {
	if f.createConnErr != nil {
		return nil, f.createConnErr
	}
	return f.created, nil
}

func (f *fakeSeedRepo) ListRulesByConnection(_ context.Context, _ string) ([]idp.StoredRule, error) {
	return f.rules, f.listErr
}

func (f *fakeSeedRepo) DeleteRule(_ context.Context, ruleID string) (bool, error) {
	if f.deleteErr != nil {
		return false, f.deleteErr
	}
	f.deletedRules = append(f.deletedRules, ruleID)
	return true, nil
}

func (f *fakeSeedRepo) CreateRule(_ context.Context, _ string, in idp.CreateRuleInput) (*idp.StoredRule, error) {
	if f.createRuleErr != nil {
		return nil, f.createRuleErr
	}
	f.createdRules = append(f.createdRules, in)
	return &idp.StoredRule{ID: "r"}, nil
}

func TestSeedZitadelRules_createsConnection(t *testing.T) {
	repo := &fakeSeedRepo{created: &idp.PublicConnection{ID: "conn-9"}}
	var out bytes.Buffer
	if err := SeedZitadelRules(context.Background(), SeedDeps{Repo: repo, SigningSecret: "s", Out: &out}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !strings.Contains(out.String(), "created zitadel connection") {
		t.Errorf("expected creation message:\n%s", out.String())
	}
	if len(repo.createdRules) != 2 {
		t.Fatalf("createdRules = %d, want 2", len(repo.createdRules))
	}
	if repo.createdRules[0].EventType != "user.grant.added" || repo.createdRules[0].Op != "write" {
		t.Errorf("rule[0] = %+v, want added/write", repo.createdRules[0])
	}
	if repo.createdRules[1].EventType != "user.grant.removed" || repo.createdRules[1].Op != "delete" {
		t.Errorf("rule[1] = %+v, want removed/delete", repo.createdRules[1])
	}
	if string(repo.createdRules[0].Match) != "[]" {
		t.Errorf("match = %q, want []", string(repo.createdRules[0].Match))
	}
	if repo.createdRules[0].TupleTemplate != teamMembership {
		t.Errorf("template = %+v, want %+v", repo.createdRules[0].TupleTemplate, teamMembership)
	}
}

func TestSeedZitadelRules_existingConnectionClears(t *testing.T) {
	repo := &fakeSeedRepo{
		conn:  &idp.Connection{ID: "conn-1"},
		rules: []idp.StoredRule{{ID: "a"}, {ID: "b"}},
	}
	var out bytes.Buffer
	if err := SeedZitadelRules(context.Background(), SeedDeps{Repo: repo, SigningSecret: "s", Out: &out}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !strings.Contains(out.String(), "zitadel connection exists") {
		t.Errorf("expected exists message:\n%s", out.String())
	}
	if len(repo.deletedRules) != 2 {
		t.Errorf("deletedRules = %d, want 2 (clear-then-insert)", len(repo.deletedRules))
	}
}

func TestSeedZitadelRules_errors(t *testing.T) {
	sentinel := errors.New("boom")
	cases := map[string]*fakeSeedRepo{
		"get":        {getErr: sentinel},
		"createConn": {createConnErr: sentinel},
		"list":       {conn: &idp.Connection{ID: "c"}, listErr: sentinel},
		"delete":     {conn: &idp.Connection{ID: "c"}, rules: []idp.StoredRule{{ID: "a"}}, deleteErr: sentinel},
		"createRule": {conn: &idp.Connection{ID: "c"}, createRuleErr: sentinel},
	}
	for name, repo := range cases {
		t.Run(name, func(t *testing.T) {
			err := SeedZitadelRules(context.Background(), SeedDeps{Repo: repo, SigningSecret: "s"})
			if !errors.Is(err, sentinel) {
				t.Fatalf("err = %v, want sentinel", err)
			}
		})
	}
}
