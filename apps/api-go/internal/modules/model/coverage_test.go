package model

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestErrBadDiffKind(t *testing.T) {
	if _, err := (DiffChange{Kind: "BOGUS"}).MarshalJSON(); err == nil || err.Error() != "model: invalid DiffChange kind" {
		t.Fatalf("err=%v", err)
	}
}

func TestSplitPair_noSeparator(t *testing.T) {
	rel, pt := splitPair("nonul")
	if rel != "nonul" || pt != "" {
		t.Fatalf("got %q %q", rel, pt)
	}
	rel, pt = splitPair("parent\x00folder")
	if rel != "parent" || pt != "folder" {
		t.Fatalf("got %q %q", rel, pt)
	}
}

func TestPublish_compilerNonCompileError(t *testing.T) {
	// compiler가 CompileError가 아닌 raw error를 내면 Hono 기본 500으로 전파된다.
	comp := fakeCompiler{err: errors.New("plain compiler failure")}
	deps := adminDeps(okStore(), &fakeGateway{}, comp, &fakeRecorder{})
	body := `{"ir":` + string(fixtureIRBytes(t)) + `}`
	w := do(t, newRouter(deps), http.MethodPost, "/model", body)
	if w.Code != 500 || w.Body.String() != "Internal Server Error" {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPublishModel_directValidation(t *testing.T) {
	// publishModel을 직접 호출해 검증 실패 detail 형태를 확인(라우트 경유 없이).
	ir := loadDocFolderTeamIR(t)
	ir.Resources[0].Permissions[0].GrantedByRoles = []string{"ghost"} // unknown role.
	_, err := publishModel(context.Background(), adminDeps(okStore(), &fakeGateway{}, fakeCompiler{}, &fakeRecorder{}), ir, nil, nil, "admin")
	var pe *PublishError
	if !errors.As(err, &pe) || pe.Status != 422 {
		t.Fatalf("err=%v", err)
	}
}
