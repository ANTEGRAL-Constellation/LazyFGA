# lazyFGA

> **An authorization control plane you can _draw_, on top of [OpenFGA](https://openfga.dev).**
> Design the model as nodes, assemble attribute conditions as blocks, publish it, and ask
> "can this user do this?" over a standard [AuthZEN](https://openid.github.io/authzen/) API — with
> a human-readable reason for every decision. Self-hosted.

See [CONCEPT.md](./CONCEPT.md) for the why, [ARCHITECTURE.md](./ARCHITECTURE.md) for the how, and
[ROADMAP.md](./ROADMAP.md) for the milestone plan. Implementation specs live in `proposals/`.

## Status

M0–M7 implemented (`proposals/lazyfga-0` … `lazyfga-19`): monorepo + self-host stack, the
visual IR ↔ OpenFGA DSL compiler, the node canvas + permission matrix, named-policy AuthZEN PDP,
explainability, the WAF-style condition builder compiling to CEL, the IdP webhook core + ZITADEL
adapter, a DB-backed audit log, and an inline playground.

## The five pillars → where they live

| Pillar                                      | Code                                                                                                                         |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 1. Node-based model authoring               | `apps/web/src/features/model-canvas` + `apps/web/src/features/permission-matrix` + `packages/compiler`                       |
| 2. WAF-style attribute conditions → CEL     | `apps/web/src/features/condition-builder` + `packages/shared/src/condition.ts` + `packages/compiler/src/condition-to-cel.ts` |
| 3. Named policy as a PDP (AuthZEN)          | `apps/api/src/modules/policy` + `apps/api/src/modules/pdp`                                                                   |
| 4. Explainability (allow path / deny links) | `apps/api/src/modules/pdp/reason.ts` + `apps/web/src/features/explain` + `apps/web/src/features/playground`                  |
| 5. IdP-agnostic identity sync               | `apps/api/src/modules/idp` (+ `adapters/zitadel.ts`)                                                                         |
| Ops (audit, tokens)                         | `apps/api/src/modules/audit` + `apps/api/src/modules/auth`                                                                   |

## Quickstart

Requires [Bun](https://bun.sh), [pnpm](https://pnpm.io), and Docker.

```bash
pnpm install

# 1. dependencies (OpenFGA + Postgres). Compose defaults: openfga :8080, postgres :5432.
#    If those ports are taken, set OPENFGA_HTTP_PORT / OPENFGA_PLAYGROUND_PORT in a root .env.
docker compose up -d postgres openfga

# 2. api (control plane + PDP). ADMIN_TOKEN guards the control plane; pick any secret.
cd apps/api
DATABASE_URL=postgres://lazyfga:lazyfga@localhost:5432/lazyfga \
OPENFGA_API_URL=http://localhost:8080 \
ADMIN_TOKEN=dev-admin-token \
bun run src/index.ts            # listens on :8787, runs migrations + bootstraps the store

# 3. web (visual studio) — in another shell
pnpm --filter @lazyfga/web dev  # vite on :5173, proxies /api → :8787
```

Open the studio at <http://localhost:5173>: draw the model, paint the role×permission matrix,
build conditions, run playground assertions, and view the audit log (audit needs the admin token).

## Run the demo

With the api running (admin token `dev-admin-token`), the self-contained demo publishes a model,
seeds a policy + a ZITADEL mapping rule, replays a **signed** ZITADEL grant webhook, seeds the
structural relationships, and proves an end-to-end ALLOW with a reason:

```bash
cd apps/api
DATABASE_URL=postgres://lazyfga:lazyfga@localhost:5432/lazyfga \
OPENFGA_API_URL=http://localhost:8080 \
ADMIN_TOKEN=dev-admin-token API_BASE=http://localhost:8787 \
bun run scripts/demo/run.ts
# → decision: ALLOW
# → reason:   user:alice can read document:report1: inherited via parent from
#             folder:reports → role viewer (via team:eng membership)

# reset the demo data (keeps the OpenFGA store/model)
bun run scripts/demo/reset.ts
```

No live ZITADEL is needed: the demo signs the webhook with the connection's signing key using the
same HMAC the adapter verifies. See [docs/getting-started.md](./docs/getting-started.md) for a
guided walkthrough and [docs/api.md](./docs/api.md) for the REST surface.

## Layout

```
apps/web        Vite + React studio (canvas, matrix, conditions, explain, playground, audit)
apps/api        Hono on Bun: modular monolith (model · policy · pdp · idp · audit · auth · openfga)
packages/shared end-to-end type contracts (model IR, condition, authzen, policy, reason, audit)
packages/compiler  the heart: ModelIR ↔ OpenFGA DSL + condition ↔ CEL (isomorphic, dep-free)
proposals/      LFGA-N implementation specs
```

## Develop

```bash
pnpm -r typecheck   # tsc across packages
pnpm -r lint        # eslint
pnpm -r test        # bun test (shared / compiler / api)
```
