import { createHmac } from "node:crypto";
import { describe, expect, test } from "bun:test";
import { verifyWebhookSignature, type WebhookSignatureSpec } from "./signature";
import { PRESETS } from "./presets";

const enc = (o: unknown): Uint8Array => new TextEncoder().encode(JSON.stringify(o));
const body = enc({ event_type: "user.grant.added", event_payload: { userId: "alice" } });
const nowSec = (): number => Math.floor(Date.now() / 1000);

// ── ZITADEL (kv_t_v, seconds, hex, raw secret) — preset에서 직접 ──
describe("verifyWebhookSignature — ZITADEL preset (kv_t_v, seconds)", () => {
  const spec = PRESETS.zitadel!.signature;
  const SECRET = "topsecret";
  const sign = (raw: Uint8Array, secret: string, t: number): string =>
    `t=${t},v1=${createHmac("sha256", secret).update(`${t}.`).update(raw).digest("hex")}`;
  const hdr = (v: string): Headers => new Headers({ "ZITADEL-Signature": v });

  test("accepts a freshly signed request", () => {
    expect(verifyWebhookSignature(spec, body, hdr(sign(body, SECRET, nowSec())), SECRET)).toBe(
      true,
    );
  });
  test("rejects tampered body / wrong secret", () => {
    const t = nowSec();
    expect(verifyWebhookSignature(spec, enc({ evil: 1 }), hdr(sign(body, SECRET, t)), SECRET)).toBe(
      false,
    );
    expect(verifyWebhookSignature(spec, body, hdr(sign(body, SECRET, t)), "other")).toBe(false);
  });
  test("rejects stale and far-future timestamps (both-sided)", () => {
    expect(
      verifyWebhookSignature(spec, body, hdr(sign(body, SECRET, nowSec() - 301)), SECRET),
    ).toBe(false);
    expect(
      verifyWebhookSignature(spec, body, hdr(sign(body, SECRET, nowSec() + 301)), SECRET),
    ).toBe(false);
  });
  test("rejects float timestamp / missing / empty-or-nonhex v1", () => {
    const t = nowSec();
    expect(verifyWebhookSignature(spec, body, hdr(`t=${t}.5,v1=${"a".repeat(64)}`), SECRET)).toBe(
      false,
    );
    expect(verifyWebhookSignature(spec, body, new Headers(), SECRET)).toBe(false);
    expect(verifyWebhookSignature(spec, body, hdr(`t=${t},v1=`), SECRET)).toBe(false);
    expect(verifyWebhookSignature(spec, body, hdr(`t=${t},v1=zzzz`), SECRET)).toBe(false);
  });

  test("rejects non-strict-decimal timestamps even if a valid HMAC is supplied (review: strict ts)", () => {
    // ".0" and "e0" pass Number.isInteger but ZITADEL's strconv.ParseInt rejects them.
    const t = nowSec();
    const good = createHmac("sha256", SECRET).update(`${t}.`).update(body).digest("hex");
    expect(verifyWebhookSignature(spec, body, hdr(`t=${t}.0,v1=${good}`), SECRET)).toBe(false);
    expect(verifyWebhookSignature(spec, body, hdr(`t=${t}e0,v1=${good}`), SECRET)).toBe(false);
    expect(verifyWebhookSignature(spec, body, hdr(`t=0${t},v1=${good}`), SECRET)).toBe(false);
  });
  test("accepts when one of multiple v1 signatures matches (key rotation)", () => {
    const t = nowSec();
    const good = createHmac("sha256", SECRET).update(`${t}.`).update(body).digest("hex");
    expect(
      verifyWebhookSignature(spec, body, hdr(`t=${t},v1=${"0".repeat(64)},v1=${good}`), SECRET),
    ).toBe(true);
  });
  test("header name is case-insensitive", () => {
    const v = sign(body, SECRET, nowSec());
    expect(
      verifyWebhookSignature(spec, body, new Headers({ "zitadel-signature": v }), SECRET),
    ).toBe(true);
  });
});

// ── Stripe-style (kv_t_v) — ad-hoc spec, 같은 엔진 ──
describe("verifyWebhookSignature — Stripe-style (kv_t_v)", () => {
  const spec: WebhookSignatureSpec = {
    header: "Stripe-Signature",
    headerFormat: "kv_t_v",
    timestampSource: "signature_header",
    timestampUnit: "seconds",
    payloadTemplate: "{timestamp}.{body}",
    algorithm: "sha256",
    encoding: "hex",
    secretEncoding: "raw",
    toleranceSec: 300,
    allowMultipleSignatures: true,
  };
  const SECRET = "whsec_stripe";
  test("accepts a valid signature", () => {
    const t = nowSec();
    const sig = createHmac("sha256", SECRET).update(`${t}.`).update(body).digest("hex");
    expect(
      verifyWebhookSignature(
        spec,
        body,
        new Headers({ "Stripe-Signature": `t=${t},v1=${sig}` }),
        SECRET,
      ),
    ).toBe(true);
  });
});

// ── Standard Webhooks (base64, separate ts header, whsec_ base64 secret) — preset ──
describe("verifyWebhookSignature — Standard Webhooks preset", () => {
  const spec = PRESETS["standard-webhooks"]!.signature;
  const SECRET = "whsec_c2VjcmV0a2V5"; // base64("secretkey") after prefix
  const key = Buffer.from("c2VjcmV0a2V5", "base64");
  const swBody = enc({ type: "user.created", data: { id: "u1" } });

  const sign = (id: string, t: number): Headers => {
    const sig = createHmac("sha256", key).update(`${id}.${t}.`).update(swBody).digest("base64");
    return new Headers({
      "webhook-id": id,
      "webhook-timestamp": String(t),
      "webhook-signature": `v1,${sig}`,
    });
  };

  test("accepts a valid Standard Webhooks signature", () => {
    expect(verifyWebhookSignature(spec, swBody, sign("msg_1", nowSec()), SECRET)).toBe(true);
  });
  test("rejects wrong id (id is part of the signed payload)", () => {
    const h = sign("msg_1", nowSec());
    h.set("webhook-id", "msg_tampered");
    expect(verifyWebhookSignature(spec, swBody, h, SECRET)).toBe(false);
  });
  test("rejects stale timestamp", () => {
    expect(verifyWebhookSignature(spec, swBody, sign("msg_1", nowSec() - 301), SECRET)).toBe(false);
  });
  test("rejects missing timestamp header", () => {
    const h = sign("msg_1", nowSec());
    h.delete("webhook-timestamp");
    expect(verifyWebhookSignature(spec, swBody, h, SECRET)).toBe(false);
  });
});

// ── GitHub (scheme_hex, no timestamp) — ad-hoc spec ──
describe("verifyWebhookSignature — GitHub-style (scheme_hex, no timestamp)", () => {
  const spec: WebhookSignatureSpec = {
    header: "X-Hub-Signature-256",
    headerFormat: "scheme_hex",
    timestampSource: "none",
    timestampUnit: "none",
    payloadTemplate: "{body}",
    algorithm: "sha256",
    encoding: "hex",
    secretEncoding: "raw",
    toleranceSec: 300,
    allowMultipleSignatures: false,
  };
  const SECRET = "ghsecret";
  test("accepts sha256=<hex> over the raw body", () => {
    const sig = createHmac("sha256", SECRET).update(body).digest("hex");
    expect(
      verifyWebhookSignature(
        spec,
        body,
        new Headers({ "X-Hub-Signature-256": `sha256=${sig}` }),
        SECRET,
      ),
    ).toBe(true);
  });
  test("rejects a tampered body", () => {
    const sig = createHmac("sha256", SECRET).update(body).digest("hex");
    expect(
      verifyWebhookSignature(
        spec,
        enc({ evil: 1 }),
        new Headers({ "X-Hub-Signature-256": `sha256=${sig}` }),
        SECRET,
      ),
    ).toBe(false);
  });

  test("rejects a mismatched scheme label (review: md5= not compared as sha256)", () => {
    const sig = createHmac("sha256", SECRET).update(body).digest("hex");
    expect(
      verifyWebhookSignature(
        spec,
        body,
        new Headers({ "X-Hub-Signature-256": `md5=${sig}` }),
        SECRET,
      ),
    ).toBe(false);
  });
});
