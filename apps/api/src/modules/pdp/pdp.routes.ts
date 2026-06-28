import { Hono } from "hono";
import { requireRole, type AppEnv } from "../../middleware/auth";
import { evaluate, EvaluateError } from "./pdp.evaluator";

export const pdpRoutes = new Hono<AppEnv>();

// PDP는 신뢰된 호출자만(service 또는 admin). end user 직접 호출 금지.
pdpRoutes.use("*", requireRole("service", "admin"));

// POST /access/v1/evaluation (AuthZEN 1.0)
pdpRoutes.post("/evaluation", async (c) => {
  const b = await c.req.json().catch(() => null);
  const str = (v: unknown): v is string => typeof v === "string" && v.length > 0;
  const ok =
    b &&
    str(b.subject?.type) &&
    str(b.subject?.id) &&
    str(b.action?.name) &&
    str(b.resource?.type) &&
    str(b.resource?.id);
  if (!ok) {
    return c.json(
      { error: "subject{type,id}, action{name}, resource{type,id} are required (non-empty)" },
      400,
    );
  }
  try {
    const res = await evaluate({
      subject: { type: b.subject.type, id: b.subject.id },
      action: { name: b.action.name },
      resource: { type: b.resource.type, id: b.resource.id },
      context: b.context && typeof b.context === "object" ? b.context : undefined,
      options: b.options && typeof b.options === "object" ? b.options : undefined,
    });
    return c.json(res);
  } catch (e) {
    if (e instanceof EvaluateError) return c.json({ error: "evaluation failed" }, 500);
    throw e;
  }
});
