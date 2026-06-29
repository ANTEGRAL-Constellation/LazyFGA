import {
  grantRequestSchema,
  parseGrantSubject,
  parseResourceRef,
  revokeRequestSchema,
} from "@lazyfga/shared";
import { Hono, type Context } from "hono";
import { requireRole, type AppEnv } from "../../middleware/auth";
import { principalActor } from "../audit/audit";
import {
  GrantError,
  grant,
  listByResource,
  listBySubject,
  revoke,
} from "./permission.service";

// lazyfga-20: 구조적 권한 grant/revoke/list. 전부 admin 전용.
export const permissionRoutes = new Hono<AppEnv>();
permissionRoutes.use("*", requireRole("admin"));

const onError = (c: Context<AppEnv>, e: unknown): Response => {
  if (e instanceof GrantError) return c.json({ error: e.detail, code: e.code }, e.status);
  throw e;
};

// POST /grants — 단일 배정 tuple write. 201(신규) | 200(이미 존재, no-op).
permissionRoutes.post("/", async (c) => {
  const body = await c.req.json().catch(() => null);
  const parsed = grantRequestSchema.safeParse(body);
  if (!parsed.success) {
    return c.json({ error: "malformed grant request", code: "malformed_request" }, 400);
  }
  try {
    const { created } = await grant(parsed.data, principalActor(c.get("principal")));
    return c.json({ granted: true, created }, created ? 201 : 200);
  } catch (e) {
    return onError(c, e);
  }
});

// DELETE /grants — 단일 배정 tuple delete. 200(삭제 | 이미 없음, no-op).
permissionRoutes.delete("/", async (c) => {
  const body = await c.req.json().catch(() => null);
  const parsed = revokeRequestSchema.safeParse(body);
  if (!parsed.success) {
    return c.json({ error: "malformed revoke request", code: "malformed_request" }, 400);
  }
  try {
    const { deleted } = await revoke(parsed.data, principalActor(c.get("principal")));
    return c.json({ revoked: true, deleted }, 200);
  } catch (e) {
    return onError(c, e);
  }
});

// GET /grants?resource=<type>:<id>  |  ?subject=<type>:<id>[#<relation>][&resourceType=<t>]
permissionRoutes.get("/", async (c) => {
  const resource = c.req.query("resource");
  const subject = c.req.query("subject");
  if ((resource && subject) || (!resource && !subject)) {
    return c.json(
      { error: "supply exactly one of `resource` or `subject`", code: "malformed_request" },
      400,
    );
  }
  try {
    if (resource) {
      const ref = parseResourceRef(resource);
      if (!ref) return c.json({ error: `invalid resource "${resource}"`, code: "malformed_request" }, 400);
      return c.json({ grants: await listByResource(ref) });
    }
    const subj = parseGrantSubject(subject!);
    if (!subj) return c.json({ error: `invalid subject "${subject}"`, code: "malformed_request" }, 400);
    const resourceType = c.req.query("resourceType");
    if (resourceType !== undefined && !/^[a-zA-Z0-9_]+$/.test(resourceType)) {
      return c.json({ error: `invalid resourceType "${resourceType}"`, code: "malformed_request" }, 400);
    }
    return c.json({ grants: await listBySubject(subj, resourceType) });
  } catch (e) {
    return onError(c, e);
  }
});
