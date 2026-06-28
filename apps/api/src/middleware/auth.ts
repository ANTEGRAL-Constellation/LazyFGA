import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import type { MiddlewareHandler } from "hono";
import { config } from "../config";
import { findActiveByHash, touchLastUsed } from "../modules/auth/token.repo";

export interface Principal {
  role: "admin" | "service";
  tokenId?: string;
}

/** 모든 Hono 라우터/앱이 공유하는 환경(인증된 principal). */
export type AppEnv = { Variables: { principal: Principal } };

const sha256hex = (s: string): string => createHash("sha256").update(s).digest("hex");

/** 길이 고정(64 hex)에서 상수시간 비교. */
function safeEqualHex(a: string, b: string): boolean {
  const ba = Buffer.from(a, "hex");
  const bb = Buffer.from(b, "hex");
  return ba.length === bb.length && timingSafeEqual(ba, bb);
}

const bearer = (header: string | undefined): string | null => {
  if (!header) return null;
  const m = /^Bearer\s+(.+)$/i.exec(header.trim());
  return m?.[1]?.trim() ?? null;
};

/** 랜덤 service token 생성. 평문은 호출자에 1회만 노출, DB엔 sha256만 저장. */
export function generateToken(): { plain: string; hash: string } {
  const plain = randomBytes(32).toString("base64url");
  return { plain, hash: sha256hex(plain) };
}

export class AuthError extends Error {
  constructor(public readonly status: 401) {
    super("unauthorized");
  }
}

/** Bearer 토큰을 Principal로 해석. 실패 시 AuthError(401). */
export async function authenticate(authorizationHeader: string | undefined): Promise<Principal> {
  const token = bearer(authorizationHeader);
  if (!token) throw new AuthError(401);

  if (config.adminToken && safeEqualHex(sha256hex(token), sha256hex(config.adminToken))) {
    return { role: "admin" };
  }

  const row = await findActiveByHash(sha256hex(token));
  if (row) {
    void touchLastUsed(row.id).catch(() => {}); // best-effort, 결정에 영향 없음
    return { role: "service", tokenId: row.id };
  }
  throw new AuthError(401);
}

/** 허용 역할 가드. 미인증=401, 역할 부족=403. 성공 시 c.set("principal"). */
export function requireRole(...roles: Principal["role"][]): MiddlewareHandler<AppEnv> {
  return async (c, next) => {
    let principal: Principal;
    try {
      principal = await authenticate(c.req.header("authorization"));
    } catch (e) {
      // 인증 실패만 401. DB 오류 등 인프라 장애는 전파해 500이 되게 한다(401로 가리지 않음).
      if (e instanceof AuthError) return c.json({ error: "unauthorized" }, 401);
      throw e;
    }
    if (!roles.includes(principal.role)) {
      return c.json({ error: "forbidden" }, 403);
    }
    c.set("principal", principal);
    await next();
  };
}
