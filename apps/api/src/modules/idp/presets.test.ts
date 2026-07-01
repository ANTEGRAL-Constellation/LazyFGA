import { createHmac } from "node:crypto";
import { describe, expect, test } from "bun:test";
import { extractEvent } from "./extraction";
import { applyEvents, type ApplyDeps, type RenderedTuple } from "./mapping";
import { PRESETS } from "./presets";
import { verifyWebhookSignature } from "./signature";
import type { MappingRule } from "./types";

const enc = (o: unknown): Uint8Array => new TextEncoder().encode(JSON.stringify(o));
const nowSec = (): number => Math.floor(Date.now() / 1000);

// 한 이벤트를 매핑 규칙으로 적용하고 생성된 tuple을 수집(write는 전부 "applied").
async function applyOne(
  ev: ReturnType<typeof extractEvent>,
  rules: MappingRule[],
): Promise<RenderedTuple[]> {
  expect(ev).not.toBeNull();
  const tuples: RenderedTuple[] = [];
  const deps: ApplyDeps = {
    writeTuple: async (_op, t) => {
      tuples.push(t);
      return "applied";
    },
    audit: () => {},
  };
  await applyEvents([ev!], rules, deps);
  return tuples;
}

describe("end-to-end — ZITADEL preset (sign → verify → extract → map → tuple)", () => {
  const preset = PRESETS.zitadel!;
  const SECRET = "dev-zitadel-signing-secret";
  const teamRule: MappingRule = {
    eventType: "user.grant.added",
    match: [],
    tupleTemplate: {
      user: "user:{{subject}}",
      relation: "member",
      object: "team:{{attributes.project}}",
    },
    op: "write",
    priority: 0,
  };

  test("grant.added → user:alice member team:eng", async () => {
    const body = enc({
      event_type: "user.grant.added",
      event_payload: { userId: "alice", projectId: "eng" },
    });
    const t = nowSec();
    const sig = `t=${t},v1=${createHmac("sha256", SECRET).update(`${t}.`).update(body).digest("hex")}`;
    expect(
      verifyWebhookSignature(
        preset.signature,
        body,
        new Headers({ "ZITADEL-Signature": sig }),
        SECRET,
      ),
    ).toBe(true);
    const ev = extractEvent(preset, JSON.parse(new TextDecoder().decode(body)));
    expect(await applyOne(ev, [teamRule])).toEqual([
      { user: "user:alice", relation: "member", object: "team:eng" },
    ]);
  });

  test("optional roleKeys fan-out → one role tuple per key", async () => {
    const ev = extractEvent(preset, {
      event_type: "user.grant.added",
      event_payload: { userId: "alice", projectId: "eng", roleKeys: ["editor", "viewer"] },
    });
    const fanRule: MappingRule = {
      eventType: "user.grant.added",
      match: [],
      tupleTemplate: {
        user: "user:{{subject}}",
        relation: "{{item}}",
        object: "project:{{attributes.project}}",
      },
      op: "write",
      fanOut: "roleKeys",
      priority: 0,
    };
    const tuples = await applyOne(ev, [fanRule]);
    expect(tuples.map((t) => t.relation).sort()).toEqual(["editor", "viewer"]);
    expect(tuples.every((t) => t.object === "project:eng")).toBe(true);
  });
});

describe("end-to-end — Standard Webhooks preset (generality proof)", () => {
  const preset = PRESETS["standard-webhooks"]!;
  const SECRET = "whsec_c2VjcmV0a2V5";
  const key = Buffer.from("c2VjcmV0a2V5", "base64");

  test("user.created → user:u1 member org:acme", async () => {
    const body = enc({ type: "user.created", data: { id: "u1", orgId: "acme" } });
    const id = "msg_1";
    const t = nowSec();
    const sig = createHmac("sha256", key).update(`${id}.${t}.`).update(body).digest("base64");
    const headers = new Headers({
      "webhook-id": id,
      "webhook-timestamp": String(t),
      "webhook-signature": `v1,${sig}`,
    });
    expect(verifyWebhookSignature(preset.signature, body, headers, SECRET)).toBe(true);
    const ev = extractEvent(preset, JSON.parse(new TextDecoder().decode(body)));
    const rule: MappingRule = {
      eventType: "user.created",
      match: [],
      tupleTemplate: {
        user: "user:{{subject}}",
        relation: "member",
        object: "org:{{attributes.org}}",
      },
      op: "write",
      priority: 0,
    };
    expect(await applyOne(ev, [rule])).toEqual([
      { user: "user:u1", relation: "member", object: "org:acme" },
    ]);
  });
});
