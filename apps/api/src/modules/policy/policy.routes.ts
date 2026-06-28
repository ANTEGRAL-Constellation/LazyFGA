import { Hono } from "hono";
import { requireRole, type AppEnv } from "../../middleware/auth";
import { deletePolicy, findById, listPolicies } from "./policy.repo";
import { createPolicy, editPolicy, PolicyError } from "./policy.service";

export const policyRoutes = new Hono<AppEnv>();

// 정책 관리는 admin 전용.
policyRoutes.use("*", requireRole("admin"));

policyRoutes.post("/", async (c) => {
  const b = await c.req.json().catch(() => null);
  if (
    !b ||
    typeof b.id !== "string" ||
    typeof b.permission !== "string" ||
    typeof b.resourceType !== "string"
  ) {
    return c.json({ error: "id, permission, resourceType are required" }, 422);
  }
  try {
    const policy = await createPolicy({
      id: b.id,
      permission: b.permission,
      resourceType: b.resourceType,
      description: typeof b.description === "string" ? b.description : undefined,
    });
    return c.json({ policy }, 201);
  } catch (e) {
    if (e instanceof PolicyError) return c.json({ error: e.detail }, e.status);
    throw e;
  }
});

policyRoutes.get("/", async (c) => c.json({ policies: await listPolicies() }));

policyRoutes.get("/:id", async (c) => {
  const p = await findById(c.req.param("id"));
  return p ? c.json({ policy: p }) : c.json({ error: "policy not found" }, 404);
});

policyRoutes.put("/:id", async (c) => {
  const id = c.req.param("id");
  if (!(await findById(id))) return c.json({ error: "policy not found" }, 404);
  const b = await c.req.json().catch(() => ({}));
  try {
    const policy = await editPolicy(id, {
      permission: typeof b?.permission === "string" ? b.permission : undefined,
      resourceType: typeof b?.resourceType === "string" ? b.resourceType : undefined,
      description: typeof b?.description === "string" ? b.description : undefined,
    });
    return c.json({ policy });
  } catch (e) {
    if (e instanceof PolicyError) return c.json({ error: e.detail }, e.status);
    throw e;
  }
});

policyRoutes.delete("/:id", async (c) => {
  const ok = await deletePolicy(c.req.param("id"));
  return ok ? c.body(null, 204) : c.json({ error: "policy not found" }, 404);
});
