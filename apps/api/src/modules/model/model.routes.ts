import { modelIrSchema } from "@lazyfga/shared";
import { Hono } from "hono";
import { diffModels } from "./diff";
import { getCurrentVersion, getVersion, listVersions } from "./model.repo";
import { publishModel, PublishError } from "./model.service";

/** auth 미들웨어(lazyfga-10)가 채울 principal. M2에선 비어 있어 createdBy="admin". */
type Vars = { Variables: { principal?: { id: string; role: string } } };

export const modelRoutes = new Hono<Vars>();

// POST /model — IR 발행(validate → compile → writeAuthModel → 버전 저장 → current 갱신).
modelRoutes.post("/", async (c) => {
  const body = await c.req.json().catch(() => null);
  const parsed = modelIrSchema.safeParse(body?.ir);
  if (!parsed.success) {
    return c.json({ error: "invalid IR shape", issues: parsed.error.issues }, 422);
  }
  const note = typeof body?.note === "string" ? body.note : undefined;
  const createdBy = c.get("principal")?.id ?? "admin";

  try {
    const version = await publishModel(parsed.data, note, createdBy);
    return c.json({ version }, 201);
  } catch (e) {
    if (e instanceof PublishError) return c.json({ error: e.message, detail: e.detail }, e.status);
    throw e;
  }
});

// GET /model/current
modelRoutes.get("/current", async (c) => {
  const v = await getCurrentVersion();
  if (!v) return c.json({ error: "no model published yet" }, 404);
  return c.json({
    version: { id: v.id, authorizationModelId: v.authorizationModelId, createdAt: v.createdAt, note: v.note },
    ir: v.irJson,
    dsl: v.dsl,
  });
});

// GET /model/versions
modelRoutes.get("/versions", async (c) => {
  const rows = await listVersions();
  return c.json({
    versions: rows.map((v) => ({
      id: v.id,
      authorizationModelId: v.authorizationModelId,
      createdAt: v.createdAt,
      note: v.note,
    })),
  });
});

// GET /model/diff?from=&to=  (정의보다 먼저 매칭되도록 :id 라우트보다 위에 둔다)
modelRoutes.get("/diff", async (c) => {
  const from = c.req.query("from");
  const to = c.req.query("to");
  if (!from || !to) return c.json({ error: "from and to query params required" }, 400);
  const [a, b] = await Promise.all([getVersion(from), getVersion(to)]);
  if (!a || !b) return c.json({ error: "version not found" }, 404);
  return c.json({ changes: diffModels(a.irJson, b.irJson) });
});

// GET /model/versions/:id
modelRoutes.get("/versions/:id", async (c) => {
  const v = await getVersion(c.req.param("id"));
  if (!v) return c.json({ error: "version not found" }, 404);
  return c.json({
    version: { id: v.id, authorizationModelId: v.authorizationModelId, createdAt: v.createdAt, note: v.note },
    ir: v.irJson,
    dsl: v.dsl,
  });
});
