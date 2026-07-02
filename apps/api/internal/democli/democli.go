// Package democli는 lazyfga-19 자체 완결 데모 오케스트레이터를 구현한다
// (TS apps/api/scripts/demo/{run,reset}.ts 포팅, LFGA-27). 라이브 ZITADEL 없이 전체 흐름을
// 한 번에 시연한다: 모델 발행(조건 1개 포함) → 정책 → IdP 연결+규칙 → 서명 webhook replay
// (grant→membership) → 구조 tuple 직접 시드(SDK) → evaluate+explain로 ALLOW 경로 시연.
//
// 모든 부수효과(HTTP·store id 조회·tuple write)는 Deps로 주입해 httptest fake로 완전히 테스트한다.
// cmd/demo/main.go는 프로덕션 어댑터(pgx·go-sdk)를 조립하는 얇은 래퍼로 남는다.
package democli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	_ "embed"

	"github.com/antegral-constellation/lazyfga/api/internal/zitadelsign"
)

// docFolderTeamIR는 데모 모델 IR fixture의 임베드 사본이다. 원본은
// packages/shared/src/__fixtures__/doc-folder-team.ir.json이며, parity 테스트가 바이트 동등을 강제한다.
//
//go:embed doc-folder-team.ir.json
var docFolderTeamIR []byte

// Doer는 데모가 필요로 하는 HTTP 클라이언트다(*http.Client가 만족; 테스트 fake 주입 지점).
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Tuple은 구조적 관계(user, relation, object)다. 데모 tuple 시드/삭제에 쓰인다.
type Tuple struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// TupleGateway는 특정 store에 구조 tuple을 write/delete한다(프로덕션은 go-sdk, 테스트는 fake).
type TupleGateway interface {
	Write(ctx context.Context, storeID string, t Tuple) error
	Delete(ctx context.Context, storeID string, t Tuple) error
}

// Deps는 데모 실행 의존성이다. 각 필드는 테스트에서 fake로 주입 가능하다.
type Deps struct {
	// APIBase는 lazyFGA API 베이스 URL(예: http://localhost:8787).
	APIBase string
	// AdminToken은 control-plane admin 토큰.
	AdminToken string
	// SigningSecret은 zitadel webhook 서명 시크릿.
	SigningSecret string
	// HTTP는 API 호출용 클라이언트.
	HTTP Doer
	// StoreID는 부트스트랩된 OpenFGA store id를 lazyFGA DB(instance_config)에서 읽는다("" = 미부트스트랩).
	StoreID func(ctx context.Context) (string, error)
	// Tuples는 구조 tuple write/delete 게이트웨이.
	Tuples TupleGateway
	// Now는 서명 replay 타임스탬프용 클록(기본 time.Now).
	Now func() time.Time
	// Out은 데모 출력 대상(기본 os.Stdout — cmd에서 주입).
	Out io.Writer
}

func (d Deps) withDefaults() Deps {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Out == nil {
		d.Out = io.Discard
	}
	return d
}

// emitf는 데모 출력 writer에 쓴다(출력 오류는 데모 진행과 무관하므로 무시).
func emitf(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// logStep은 run.ts의 log() 헬퍼를 재현한다(선행 개행 + "▶ " 프리픽스 + 후행 개행).
func logStep(out io.Writer, msg string) {
	emitf(out, "\n▶ %s\n", msg)
}

// Run은 데모 전체 흐름을 실행한다(run.ts step-for-step 포팅).
func Run(ctx context.Context, deps Deps) error {
	deps = deps.withDefaults()
	out := deps.Out
	c := &apiClient{base: deps.APIBase, admin: deps.AdminToken, http: deps.HTTP}

	// 0) 스택 선검사.
	status, _, err := c.get(ctx, "/healthz")
	if err != nil || status < 200 || status >= 300 {
		return fmt.Errorf("api not ready at %s (start api + openfga + postgres first)", deps.APIBase)
	}

	// 1) 데모 모델 발행: docFolderTeamIR + non_expired 조건을 document.owner 부여에 부착.
	//    (조건은 시연 allow 경로(viewer 상속) 밖이라 evaluate에 context가 없어도 ALLOW가 난다.)
	ir, err := demoIR()
	if err != nil {
		return err
	}
	logStep(out, "publish demo model (with a non_expired condition on document.owner)")
	pubBody, err := json.Marshal(map[string]any{"ir": ir, "note": "demo (lazyfga-19)"})
	if err != nil {
		return err
	}
	status, respBody, err := c.postAdmin(ctx, "/model", pubBody)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("model publish failed: %d %s", status, string(respBody))
	}

	// 2) 정책 시드(이미 있으면 409 → 무시).
	logStep(out, "seed policy can-read-doc (read, document)")
	if _, _, err := c.postAdmin(ctx, "/policies", []byte(policyBody)); err != nil {
		return err
	}

	// 3) IdP 연결 + 규칙 시드(idempotent). 연결이 있으면 secret을 데모 값으로 PUT(서명 일치 보장).
	logStep(out, "seed zitadel connection + projectId-based mapping rule")
	connID, err := c.seedConnection(ctx, deps.SigningSecret)
	if err != nil {
		return err
	}
	if err := c.seedRule(ctx, connID); err != nil {
		return err
	}

	// 4) 서명된 ZITADEL grant 이벤트 replay → user:alice member team:eng (webhook 경로, audited).
	//    실제 ZITADEL shape: usergrant-aggregate → 주체는 event_payload.userId, 타임스탬프는 초.
	logStep(out, "replay a signed ZITADEL user.grant.added webhook (alice granted project 'eng')")
	raw, err := json.Marshal(webhookPayload{
		EventType: "user.grant.added",
		EventPayload: grantPayload{
			UserID:    "alice",
			ProjectID: "eng",
		},
	})
	if err != nil {
		return err
	}
	sig := zitadelsign.Header(raw, deps.SigningSecret, deps.Now().Unix())
	whStatus, whBody, err := c.request(ctx, http.MethodPost, "/idp/webhook/zitadel",
		map[string]string{"ZITADEL-Signature": sig, "Content-Type": "application/json"}, raw)
	if err != nil {
		return err
	}
	emitf(out, "   webhook → %d %s\n", whStatus, string(whBody))

	// 5) 구조 tuple 직접 시드(매핑 엔진으로는 userset/parent를 못 쓰므로 SDK로 직접; Q4=A).
	logStep(out, "seed structural tuples via OpenFGA SDK (team→folder role binding + folder→document parent)")
	storeID, err := deps.StoreID(ctx)
	if err != nil {
		return err
	}
	if storeID == "" {
		return errors.New("instance_config missing (api not bootstrapped?)")
	}
	structural := []Tuple{
		{User: "team:eng#member", Relation: "viewer", Object: "folder:reports"},
		{User: "folder:reports", Relation: "parent", Object: "document:report1"},
	}
	for _, t := range structural {
		if err := deps.Tuples.Write(ctx, storeID, t); err != nil {
			// 멱등(이미 존재)만 조용히 넘기고, 실제 오류(연결 거부/모델 오류 등)는 표면화한다.
			if !isIdempotentTupleErr(err) {
				emitf(out, "   ! structural tuple write failed: %s → %s\n", tupleJSON(t), err.Error())
			}
		}
	}

	// 6) evaluate + explain: alice가 document:report1을 read할 수 있나? (grant→team→상속 경로)
	logStep(out, "evaluate: can user:alice read document:report1 ? (path: grant → team → folder → document)")
	_, evalRespBody, err := c.postAdmin(ctx, "/access/v1/evaluation", []byte(evalRequestBody))
	if err != nil {
		return err
	}
	var decision evalResponse
	_ = json.Unmarshal(evalRespBody, &decision) // 파싱 실패 시 zero값(DENY / (none))으로 표시.
	verdict := "DENY"
	if decision.Decision {
		verdict = "ALLOW"
	}
	emitf(out, "   decision: %s\n", verdict)
	reason := decision.Context.Reason.Text
	if reason == "" {
		reason = "(none)"
	}
	emitf(out, "   reason:   %s\n", reason)

	emitf(out, "\n✔ demo complete. Explore in the web studio (canvas / conditions / playground / audit).\n")
	return nil
}

// Reset은 데모 상태를 초기화한다(reset.ts 포팅). 정책·IdP 설정·데모 tuple을 정리한다.
// (OpenFGA store/authorization model 자체는 남긴다 — 재발행은 Run이 한다.)
func Reset(ctx context.Context, deps Deps) error {
	deps = deps.withDefaults()
	c := &apiClient{base: deps.APIBase, admin: deps.AdminToken, http: deps.HTTP}

	// 정책 삭제(없으면 404 무시).
	if _, _, err := c.deleteAdmin(ctx, "/policies/can-read-doc"); err != nil {
		return err
	}

	// IdP zitadel 연결 삭제(규칙 cascade).
	status, body, err := c.getAdmin(ctx, "/idp/connections")
	if err != nil {
		return err
	}
	if status >= 200 && status < 300 {
		var list connectionList
		if err := json.Unmarshal(body, &list); err != nil {
			return err
		}
		for _, conn := range list.Connections {
			if conn.Provider == "zitadel" {
				if _, _, err := c.deleteAdmin(ctx, "/idp/connections/"+conn.ID); err != nil {
					return err
				}
				break
			}
		}
	}

	// 데모 tuple 삭제(SDK; 없으면 무시). store id가 없으면(미부트스트랩) 건너뛴다.
	storeID, err := deps.StoreID(ctx)
	if err != nil {
		return err
	}
	if storeID != "" {
		deletes := []Tuple{
			{User: "user:alice", Relation: "member", Object: "team:eng"},
			{User: "team:eng#member", Relation: "viewer", Object: "folder:reports"},
			{User: "folder:reports", Relation: "parent", Object: "document:report1"},
		}
		for _, t := range deletes {
			_ = deps.Tuples.Delete(ctx, storeID, t) // not found: ignore.
		}
	}
	emitf(deps.Out, "demo state reset (policy + idp config + demo tuples cleared)\n")
	return nil
}

// ── webhook payload / eval 응답 형태 ────────────────────────────────────────────

// webhookPayload는 ZITADEL grant 이벤트 페이로드다(JS JSON.stringify 키 순서 보존용 struct).
type webhookPayload struct {
	EventType    string       `json:"event_type"`
	EventPayload grantPayload `json:"event_payload"`
}

type grantPayload struct {
	UserID    string `json:"userId"`
	ProjectID string `json:"projectId"`
}

type evalResponse struct {
	Decision bool `json:"decision"`
	Context  struct {
		Reason struct {
			Text string `json:"text"`
		} `json:"reason"`
	} `json:"context"`
}

type connectionList struct {
	Connections []struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	} `json:"connections"`
}

// isIdempotentTupleErr은 멱등(이미 존재/중복 write) 오류인지 판별한다(run.ts substring 매칭 포팅).
func isIdempotentTupleErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "write_failed_due_to_invalid_input")
}

// tupleJSON은 tuple을 JSON.stringify(t)와 동일하게 직렬화한다(경고 메시지용).
func tupleJSON(t Tuple) string {
	b, _ := json.Marshal(t)
	return string(b)
}

// ── HTTP 클라이언트 ─────────────────────────────────────────────────────────────

type apiClient struct {
	base  string
	admin string
	http  Doer
}

func (c *apiClient) request(ctx context.Context, method, path string, headers map[string]string, body []byte) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBody, nil
}

func (c *apiClient) adminHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + c.admin, "Content-Type": "application/json"}
}

func (c *apiClient) get(ctx context.Context, path string) (int, []byte, error) {
	return c.request(ctx, http.MethodGet, path, nil, nil)
}

func (c *apiClient) getAdmin(ctx context.Context, path string) (int, []byte, error) {
	return c.request(ctx, http.MethodGet, path, c.adminHeaders(), nil)
}

func (c *apiClient) postAdmin(ctx context.Context, path string, body []byte) (int, []byte, error) {
	return c.request(ctx, http.MethodPost, path, c.adminHeaders(), body)
}

func (c *apiClient) putAdmin(ctx context.Context, path string, body []byte) (int, []byte, error) {
	return c.request(ctx, http.MethodPut, path, c.adminHeaders(), body)
}

func (c *apiClient) deleteAdmin(ctx context.Context, path string) (int, []byte, error) {
	return c.request(ctx, http.MethodDelete, path, c.adminHeaders(), nil)
}

// seedConnection은 zitadel 연결을 idempotent하게 보장하고 그 id를 돌려준다.
// 201이면 새로 만든 연결 id, 아니면 기존 연결을 찾아 secret을 데모 값으로 PUT한다.
func (c *apiClient) seedConnection(ctx context.Context, signingSecret string) (string, error) {
	createBody, err := json.Marshal(map[string]any{"provider": "zitadel", "preset": "zitadel", "signingSecret": signingSecret})
	if err != nil {
		return "", err
	}
	status, body, err := c.postAdmin(ctx, "/idp/connections", createBody)
	if err != nil {
		return "", err
	}
	if status == http.StatusCreated {
		var env struct {
			Connection struct {
				ID string `json:"id"`
			} `json:"connection"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return "", err
		}
		return env.Connection.ID, nil
	}

	// 이미 존재: 목록에서 zitadel 연결을 찾아 secret을 데모 값으로 갱신.
	_, listBody, err := c.getAdmin(ctx, "/idp/connections")
	if err != nil {
		return "", err
	}
	var list connectionList
	if err := json.Unmarshal(listBody, &list); err != nil {
		return "", err
	}
	connID := ""
	for _, conn := range list.Connections {
		if conn.Provider == "zitadel" {
			connID = conn.ID
			break
		}
	}
	if connID == "" {
		return "", fmt.Errorf("no existing zitadel connection found (create returned %d)", status)
	}
	putBody, err := json.Marshal(map[string]any{"signingSecret": signingSecret})
	if err != nil {
		return "", err
	}
	if _, _, err := c.putAdmin(ctx, "/idp/connections/"+connID, putBody); err != nil {
		return "", err
	}
	return connID, nil
}

// seedRule은 연결의 기존 규칙을 지우고(clear-then-insert) project 기반 write 규칙 1개를 넣는다.
func (c *apiClient) seedRule(ctx context.Context, connID string) error {
	_, listBody, err := c.getAdmin(ctx, "/idp/connections/"+connID+"/rules")
	if err != nil {
		return err
	}
	var rules struct {
		Rules []struct {
			ID string `json:"id"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(listBody, &rules); err != nil {
		return err
	}
	for _, r := range rules.Rules {
		if _, _, err := c.deleteAdmin(ctx, "/idp/rules/"+r.ID); err != nil {
			return err
		}
	}
	if _, _, err := c.postAdmin(ctx, "/idp/connections/"+connID+"/rules", []byte(ruleBody)); err != nil {
		return err
	}
	return nil
}

// 정적 요청 본문(signingSecret/IR 같은 동적 값이 없는 페이로드는 상수로 둔다).
const (
	policyBody = `{"id":"can-read-doc","permission":"read","resourceType":"document"}`

	ruleBody = `{"eventType":"user.grant.added","op":"write",` +
		`"tupleTemplate":{"user":"user:{{subject}}","relation":"member","object":"team:{{attributes.project}}"}}`

	evalRequestBody = `{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},` +
		`"resource":{"type":"document","id":"report1"},"options":{"reason":true}}`
)
