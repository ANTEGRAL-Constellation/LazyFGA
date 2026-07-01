# lazyFGA

> **OpenFGA를 "그릴 수 있는" 인가(authorization) 컨트롤 플레인.**
> 모델을 노드로 설계하고, 조건을 블록으로 조립하고, 그 결과를 REST API 한 줄로 PDP처럼 꽂는다. self-hosted.

---

## 지금, 인가는 이렇게 한다

[OpenFGA](https://openfga.dev)는 Google Zanzibar 기반의 강력한 ReBAC 엔진이다. 표현력도, 성능도 충분하다. 그런데 이걸 실제로 도입하려는 사람이 마주하는 현실은 이렇다.

- **모델은 손으로 쓰는 DSL이다.** `.fga` 문법으로 `type`, `relation`, `from`, `but not`을 직접 타이핑한다. 화면의 그래프는 _내가 쓴 텍스트를 그려주는_ read-only 뷰일 뿐, 캔버스에서 노드를 끌어다 모델을 **만드는** 도구가 아니다.

- **어려움은 데이터가 아니라 "모델"에 있다.** tuple(누가 무엇에 어떤 관계인지)은 CRUD면 된다. 정작 사람이 막히는 건 `viewer from parent`(상속), `group#member`(userset), `or`/`and`/`but not` 같은 **모델 구조**다.

- **조건은 CEL을 글로 쓴다.** "업무시간에만", "사내 IP에서만" 같은 속성 기반 규칙을 코드로 작성하고, 문법 오류와 씨름한다.

- **결정은 블랙박스다.** Check는 `{ "allowed": true }`나 `false`만 돌려준다. _왜_ 허용·거부됐는지는 별도로 풀어봐야 안다.

- **IdP에 붙이려면 매번 글루 코드를 짠다.** 인증은 ZITADEL·Keycloak·Auth0가 한다. 하지만 "이 사람이 이걸 할 수 있나?"를 묻는 다리는 프로젝트마다 처음부터 새로 만든다.

이 다섯 가지를 **한 자리에서, 시각적으로, 표준 API로** 잇는 것이 lazyFGA다.

---

## lazyFGA가 하는 일

이 중에서도 **노드 기반 모델 저작**과 **결정 경로를 보여주는 explainability**가 대체 불가능한 핵심이다. 나머지 기능은 그 둘을 실제 운영(조건·정책 발행·IdP 연동)으로 잇는 통로다.

### 1. 노드로 그리는 모델 설계 — Visual-first authoring

캔버스에 resource 타입을 노드로 꺼내고, "속함 / 권한 상속"을 엣지로 잇는다. 노드를 더블클릭하면 그 타입 안의 **role × permission 행렬**이 열린다. 체크박스로 _"editor는 read·write 가능"_ 을 칠하면, 대응하는 OpenFGA relation이 자동으로 만들어진다.

- **매크로는 노드, 마이크로는 행렬.** 타입 사이의 관계는 그래프로(원래 그래프 모양이니까), 타입 안의 권한은 표로(원래 표 모양이니까) 다룬다.
- **캔버스 ↔ DSL은 양방향이다 — 지원 범위 안에서.** 비주얼이 표현하는 범위 안에서는 그래프를 바꾸면 DSL이, DSL을 바꾸면 그래프가 따라온다. 그 바깥의 고급 구문은 텍스트가 원본이 되고 캔버스는 read-only로 표시한다 (아래 _비주얼이 표현하는 범위_ 참고).
- **왜 쓸모 있나:** Zanzibar 모델의 학습 곡선 대부분이 사라진다. 권한을 _그림으로_ 사고하게 된다.

### 2. WAF식 조건 빌더 — Visual conditions

Cloudflare WAF 룰을 만들듯, `AND` / `OR` 블록을 선형으로 쌓아 조건을 만든다. _"업무시간 AND 사내 IP"_ 같은 규칙이 클릭으로 완성되고, 내부적으로 OpenFGA의 [CEL condition](https://openfga.dev/docs/modeling/conditions)으로 컴파일된다.

- **무엇을 만드는가 (중요):** 이 빌더가 만드는 건 **속성 조건(시간·IP·요청값)이지 권한 로직 자체가 아니다.** 누가 무엇을 할 수 있는지(role↔permission)는 모델의 행렬에서 정해지고, 조건은 그 위에 _"단, ~할 때만"_ 을 얹는다.
- **왜 쓸모 있나:** 속성 기반(ABAC) 규칙을 코드 없이, 비개발자도 읽고 만들 수 있다.

### 3. 이름 붙인 정책 = REST API 한 줄 — Named policy as a PDP

자주 쓰는 인가 질의를 **"질문의 틀(template)"로 등록**해둔다. 정책은 *어떤 권한(action)을 어떤 리소스 타입에 대해 묻는지*를 가리키는 이름(slug)이고, 호출은 표준 **AuthZEN**(`subject`·`action`·`resource`·`context`) 형태로 한다 — `resource.id`로 `document:123` 같은 **인스턴스별(fine-grained) 판단**이 그대로 된다. (slug는 라벨·관리용이며, slug로 직접 부르는 `/policy/evaluate` 단축형은 후속 편의 기능이다.)

```http
POST /access/v1/evaluation
{ "subject":  { "type": "user", "id": "antegral" },
  "action":   { "name": "read" },
  "resource": { "type": "document", "id": "123" },
  "context":  { "ip": "10.0.0.4" } }

→ { "decision": true,
    "context": { "reason": "user:antegral 은 document:123 의 부모 folder:reports 에서 viewer 를 상속받았음" } }
```

> reason은 결정 경로(구조)를 설명한다. 속성 조건('사내 IP' 등)까지의 서술은 조건 기능(M5) 이후다. object이 하나뿐인 리소스(예: AI 서버)는 고정 id(`server:main`)로 부르면 되며, "object 생략" 싱글턴 호출은 MVP 범위 밖이다.

- 응답은 표준 **[OpenID AuthZEN](https://openid.github.io/authzen/)** 인가 API 형태(`decision` + 선택적 `context`) — AuthZEN을 아는 게이트웨이·PEP와 바로 호환된다.
- **왜 쓸모 있나:** 애플리케이션은 OpenFGA의 **relation·DSL을 몰라도 된다** — 리소스 타입과 액션 이름만 알면 "이거 돼?"를 물을 수 있다.

### 4. 왜 그런 결정인지 보여준다 — Explainability

모든 결정에 사람이 읽을 수 있는 `reason`이 붙는다 — 단 허용과 거부는 성격이 다르다.

- **허용(allow):** 권한이 성립한 *경로*를 그래프 위에 보여준다 (witnessing path; 모델/데이터 경합·깊이 초과 시 best-effort 폴백).
- **거부(deny):** 보여줄 경로가 없으므로, _"가장 가까운 빠진 연결고리"_ 를 best-effort로 짚어준다 (예: _"folder:reports 의 viewer였다면 통과했음"_).
- **왜 쓸모 있나:** 인가 디버깅이 "왜 막혔지…"에서 "이 연결만 있었으면 됐군"으로 바뀐다. 동시에 모델을 배우는 도구가 된다.

### 5. 아무 IdP에나 꽂는 신원 연동 — IdP-agnostic integration

인증(authn)은 IdP가, 인가(authz)는 lazyFGA가. 둘을 잇는 길은 **시점이 다른 두 가지**다 — 섞으면 안 된다.

- **① 신원 동기화 (인증 플로우 시점):** 유저 생성·그룹/역할 부여 같은 IdP 이벤트를 **ZITADEL Actions** 같은 hook이나 webhook으로 받아 OpenFGA tuple로 떨군다. OIDC [토큰 claims를 그대로 인가 입력](https://openfga.dev/docs/modeling/token-claims-contextual-tuples)으로 쓴다. → _그래프를 신원과 최신으로 유지_
- **② 결정 호출 (요청 시점):** 앱 또는 API 게이트웨이의 PEP가 매 요청마다 위의 evaluate API를 부른다. → _allow/deny 판단_

> ZITADEL Actions는 인증 플로우에 도는 거라 ①에 맞다. 매 리소스 요청 판단인 ②는 보통 앱/게이트웨이 PEP가 맡는다 — 이 둘을 구분하는 게 중요하다.

어느 IdP든 공통분모는 **"OIDC claims + webhook"** 하나다. ZITADEL을 첫 레시피로 삼되, 같은 패턴이 Keycloak·Auth0·Okta·Cognito에 그대로 매핑된다.

- **왜 쓸모 있나:** _"Bring your IdP."_ 인가 시스템을 처음부터 만들 필요가 없다. **연동 표면은 REST 한 줄** — 단, 모델과 tuple 데이터는 여전히 내가 소유·유지한다.

### 6. 운영에 필요한 것들 — Self-hosted control plane

- **self-hosted:** 데이터도 결정도 내 인프라 안에서.
- **audit log:** 모델·tuple·정책의 모든 변경을 추적.
- **service token:** 누가 PDP에 물어볼 수 있는지 통제. (PDP는 신뢰된 호출자만 — end user 직접 노출 금지)

---

## 비주얼이 표현하는 범위 (그리고 그 경계)

비주얼-퍼스트가 _"모든 OpenFGA 모델을 그림으로"_ 라는 뜻은 아니다. 경계를 분명히 둔다.

- **100% 비주얼 (대부분의 실제 모델):** resource 타입, role↔permission, containment 기반 권한 상속, group(team) 부여, 기본 속성 조건.
- **Advanced (명시적으로 표시):** 중첩 intersection(`and`)·exclusion(`but not`), 복잡한 다중 userset 등. 여기서는 **텍스트(DSL)가 원본**이고 캔버스는 read-only로 보여준다. 양방향 sync도 이 지원 범위 안에서만 보장한다.
- **막다른 길 없음:** 비주얼로 시작해도 advanced로 내려갈 수 있고, advanced 구문이 섞여 있어도 모델 전체를 계속 시각화·검증할 수 있다.

즉 _흔한 80%는 누구나 그림으로, 어려운 20%는 숨기지 않고 안내한다._

---

## 한눈에 보는 흐름

```
        [ 캔버스에서 모델을 그린다 ]
                  │  노드 ↔ DSL 자동 컴파일 (지원 범위 안에서 양방향)
                  ▼
        [ WAF식으로 속성 조건을 얹는다 ]
                  │
                  ▼
        [ 정책을 "질문의 틀"로 이름 붙여 저장 ]
                  │  REST 한 줄로 발행
                  ▼
  ── ② 결정 (요청 시점) ───────────────────────────────
     앱 / API 게이트웨이 PEP  ──(AuthZEN)──▶  allow / deny + 왜
        ▲  evaluate(subject, object, context)

  ── ① 신원 동기화 (인증 플로우 시점) ──────────────────
     ZITADEL Actions / Keycloak · Auth0 hook
        └─ webhook · OIDC claims ──▶  OpenFGA tuple
```

**설계 → 조건 → 발행 → 연동 → 설명**이 하나의 제품 안에서 끊김 없이 이어진다.

---

## 설계 원칙

- **파생 가능한 건 생성한다.** role×permission 행렬에서 permission relation을 자동 생성한다. 사람은 도메인 결정만 내린다.
- **위험한 건 제약한다.** 기본 경로는 `union` + 상속만. intersection·exclusion·조건은 advanced 모드에서 연다.
- **추상적인 건 구체화한다.** 모든 relation을 자연어로 역번역해 보여주고, 예시(assertion)로 그 자리에서 테스트한다.
- **표준에 정렬한다.** PDP API는 OpenID AuthZEN을 따른다. 특정 도구에 락인되지 않는다.
- **막다른 길을 만들지 않는다.** 비주얼로 시작하되 언제든 DSL로 내려가고 다시 올라올 수 있다 (progressive disclosure).

---

## 누구를 위한 것인가

**1차 타깃 →** ReBAC가 필요하지만 Zanzibar DSL 학습에 시간을 못 쓰는 **백엔드 개발자** (IdP는 이미 있고, 인가만 빠르게 붙이고 싶은).

확장 타깃:

- 권한을 UI로 보고 관리하려는 **운영자 · 보안 담당**
- 사내 여러 서비스에 인가를 표준으로 깔려는 **플랫폼 팀**

---

## 지금은 범위 밖

- **자체 인증(authn) 제공** — 인증은 IdP의 몫. lazyFGA는 인가에 집중한다.
- **OpenFGA를 대체하는 새 엔진** — lazyFGA는 OpenFGA를 *대체*하지 않는다. OpenFGA _위의_ 컨트롤 플레인이다.
