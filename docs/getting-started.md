# Getting started

A guided tour of lazyFGA end to end. See the root [README](../README.md) for install/run and
[docs/api.md](./api.md) for the REST surface.

## 1. Bring it up

```bash
pnpm install
cp -n .env.example .env  # ADMIN_TOKEN을 실제 값으로 설정(파일 전체 보간 때문에 의존 서비스만 띄울 때도 필요)
docker compose up -d postgres openfga
cd apps/api-go
DATABASE_URL=postgres://lazyfga:lazyfga@localhost:5432/lazyfga \
OPENFGA_API_URL=http://localhost:8080 ADMIN_TOKEN=dev-admin-token \
go run ./cmd/lazyfga-api
# in another shell:
pnpm --filter @lazyfga/web dev
```

`GET /healthz` should report `status: ok` once the store is bootstrapped. (The backend is Go; its
directory is `apps/api-go` until the cutover swap renames it to `apps/api`. The HTTP API, port
8787, and env vars are unchanged from the former TS backend.)

## 2. Design a model (pillar 1)

In the studio (<http://localhost:5173>):

- Drag resource types onto the canvas; connect a child→parent edge for inheritance.
- Double-click a type to open its **role × permission matrix**; tick cells to grant.
- The **OpenFGA DSL** preview updates live (the compiler runs in the browser). Paste DSL to import;
  constructs outside the visual subset show read-only.

## 3. Add a condition (pillar 2)

In **Conditions**, add a condition (e.g. `non_expired`: `current_time < expiry`) using AND/OR
blocks over time / IP / value operands, then attach it to a role assignment. The DSL preview now
shows `define <role>: [user with non_expired]` plus the `condition` block. lazyFGA never evaluates
the condition itself — it compiles CEL and OpenFGA evaluates it at Check time with the request
`context`.

## 4. Publish + name a policy (pillar 3)

Publish the model (`POST /model`), then register a policy, e.g.
`{ id: "can-read-doc", permission: "read", resourceType: "document" }`. Now any caller can ask via
AuthZEN without knowing OpenFGA relations:

```bash
curl -s localhost:8787/access/v1/evaluation -H "authorization: Bearer <service-token>" \
  -H 'content-type: application/json' \
  -d '{"subject":{"type":"user","id":"alice"},"action":{"name":"read"},"resource":{"type":"document","id":"report1"}}'
```

## 5. Sync identities (pillar 5)

Register an IdP connection + mapping rules (`/idp/connections`, `/idp/connections/:id/rules`).
Point a ZITADEL Action V2 target at `POST /idp/webhook/zitadel` with the connection's signing key,
and bind the relevant events (`user.human.added`, `user.grant.*`) via an Execution. Grants then
flow in as tuples automatically.

## 6. See why (pillar 4) + test (playground)

Every decision carries a reason: an allow shows the witnessing path on the canvas; a deny shows the
nearest missing links. The **Playground** runs many assertions at once against the published model
and reuses explain per case.

## 7. The whole thing in one command

```bash
cd apps/api-go
DATABASE_URL=postgres://lazyfga:lazyfga@localhost:5432/lazyfga \
OPENFGA_API_URL=http://localhost:8080 ADMIN_TOKEN=dev-admin-token \
API_BASE=http://localhost:8787 go run ./cmd/demo run
```

This publishes a model (with a condition), seeds a policy + ZITADEL mapping rule, replays a signed
grant webhook, seeds the structural relationships, and prints an end-to-end `ALLOW` with its reason.
Every control-plane change above is recorded in the **Audit** view.
