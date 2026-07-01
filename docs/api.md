# lazyFGA API reference

Base URL defaults to `http://localhost:8787`. Auth is a bearer token:

- **admin** — the `ADMIN_TOKEN` env value. Required for the control plane.
- **service** — tokens minted via `POST /tokens`. May call the PDP.

The IdP webhook is **not** token-authed; it is verified by the provider's signature.

## Health

| Method | Path       | Auth   | Notes                                                                         |
| ------ | ---------- | ------ | ----------------------------------------------------------------------------- |
| GET    | `/healthz` | public | `{status, version, db, openfga, storeReady}`; 503 when degraded. No store id. |

## Model (admin)

| Method | Path                            | Notes                                                                                        |
| ------ | ------------------------------- | -------------------------------------------------------------------------------------------- |
| POST   | `/model`                        | Publish a model. Body `{ ir: ModelIR, note? }` → 201 `{version}`. 422 on invalid IR/compile. |
| GET    | `/model/current`                | Current published IR + version, or 404.                                                      |
| GET    | `/model/versions`               | Version history.                                                                             |
| GET    | `/model/versions/:id`           | A single version's IR + meta.                                                                |
| GET    | `/model/diff?from=<id>&to=<id>` | Diff two named versions. 400 if `from`/`to` missing; 404 if unknown.                         |

## Policies (admin)

A policy is a `(permission, resourceType)` template addressed by a slug id.

| Method         | Path            | Notes                                                                                                                                            |
| -------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| POST           | `/policies`     | `{ id, permission, resourceType, description? }` → 201. 409 on duplicate `(permission, resourceType)`. 422 if the model lacks the type/relation. |
| GET            | `/policies`     | List.                                                                                                                                            |
| GET/PUT/DELETE | `/policies/:id` | Get / update / delete.                                                                                                                           |

## PDP — AuthZEN 1.0 (service or admin)

| Method | Path                    | Notes                                                                                                                                                                                                                        |
| ------ | ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| POST   | `/access/v1/evaluation` | `{subject:{type,id}, action:{name}, resource:{type,id}, context?, options?:{reason?}}` → `{decision, context?}`. `options.reason=true` attaches a human-readable `context.reason`. No policy → `decision:false` (NO_POLICY). |

## IdP (lazyfga-15/16)

| Method     | Path                         | Auth      | Notes                                                                                                                                                                         |
| ---------- | ---------------------------- | --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| POST       | `/idp/webhook/:provider`     | signature | Verifies the provider signature, maps events → tuples. 200 `{applied, skipped, failed}`; 401 bad signature; 403 disabled; 404 unknown provider; 501 no adapter; 502 upstream. |
| POST/GET   | `/idp/connections`           | admin     | Create / list connections (signing secret never returned).                                                                                                                    |
| PUT/DELETE | `/idp/connections/:id`       | admin     | Update (secret/enabled) / delete (cascades rules).                                                                                                                            |
| GET/POST   | `/idp/connections/:id/rules` | admin     | List / create mapping rules.                                                                                                                                                  |
| PUT/DELETE | `/idp/rules/:ruleId`         | admin     | Update / delete a rule.                                                                                                                                                       |

A mapping rule: `{ eventType, match: [{field, equals}], tupleTemplate: {user, relation, object}, op: "write"|"delete", priority }`.
Templates use `{{path}}` (e.g. `{{subject.id}}`, `{{attributes.projectId}}`); the `type:` prefix must be literal and substituted values may not contain `:`, `#`, `*`, or whitespace.

## Tokens (admin)

| Method | Path          | Notes                                                                                      |
| ------ | ------------- | ------------------------------------------------------------------------------------------ |
| POST   | `/tokens`     | `{name}` → 201 `{id, name, token}`. Plaintext token shown once; only the sha256 is stored. |
| GET    | `/tokens`     | List (no hash/plaintext).                                                                  |
| DELETE | `/tokens/:id` | Revoke.                                                                                    |

## Audit (admin, lazyfga-17)

| Method | Path     | Notes                                                                                                                                                     |
| ------ | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GET    | `/audit` | `?action=&actor=&from=&to=&limit=&cursor=`. `action` supports a trailing `*` for prefix match. Newest first, keyset paginated → `{entries, nextCursor?}`. |
