import { Hono } from "hono";
import { generateToken, requireRole, type AppEnv } from "../../middleware/auth";
import { createToken, listTokens, revokeToken } from "./token.repo";

export const tokenRoutes = new Hono<AppEnv>();

// 토큰 관리는 admin 전용.
tokenRoutes.use("*", requireRole("admin"));

// POST /tokens — 발급. 평문 토큰은 이 응답에서만 1회 노출.
tokenRoutes.post("/", async (c) => {
  const body = await c.req.json().catch(() => null);
  const name = typeof body?.name === "string" ? body.name.trim() : "";
  if (!name) return c.json({ error: "name is required" }, 400);
  const { plain, hash } = generateToken();
  const row = await createToken(name, hash);
  return c.json({ id: row.id, name: row.name, token: plain }, 201);
});

// GET /tokens — 목록(해시/평문 미노출).
tokenRoutes.get("/", async (c) => {
  const rows = await listTokens();
  return c.json({
    tokens: rows.map((t) => ({
      id: t.id,
      name: t.name,
      createdAt: t.createdAt,
      lastUsedAt: t.lastUsedAt,
      revoked: t.revokedAt !== null,
    })),
  });
});

// DELETE /tokens/:id — 폐기.
tokenRoutes.delete("/:id", async (c) => {
  const ok = await revokeToken(c.req.param("id"));
  if (!ok) return c.json({ error: "token not found" }, 404);
  return c.body(null, 204);
});
