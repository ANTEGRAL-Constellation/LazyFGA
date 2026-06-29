import { describe, expect, test } from "bun:test";
import {
  applyEvents,
  matchRule,
  renderTuple,
  WriteError,
  type ApplyDeps,
  type RenderedTuple,
} from "./mapping";
import type { IdpEvent, MappingRule } from "./types";

const ev: IdpEvent = {
  type: "user.grant.added",
  subject: { id: "user:alice" },
  attributes: { projectId: "123", role: "editor" },
};

const writeRule = (over: Partial<MappingRule> = {}): MappingRule => ({
  eventType: "user.grant.added",
  match: [],
  tupleTemplate: { user: "user:{{subject.id}}", relation: "member", object: "team:{{attributes.projectId}}" },
  op: "write",
  priority: 0,
  ...over,
});

describe("matchRule", () => {
  test("matches on eventType + predicates", () => {
    expect(matchRule(writeRule({ match: [{ field: "attributes.role", equals: "editor" }] }), ev)).toBe(true);
    expect(matchRule(writeRule({ match: [{ field: "attributes.role", equals: "viewer" }] }), ev)).toBe(false);
    expect(matchRule(writeRule({ eventType: "other" }), ev)).toBe(false);
  });
});

describe("renderTuple (injection guard)", () => {
  test("substitutes placeholders into a valid tuple", () => {
    // subject.id already contains a ':' (user:alice) → would violate the value guard if used in id slot;
    // here user template is "user:{{subject.id}}" so subject.id must be a bare id.
    const e2: IdpEvent = { type: "x", subject: { id: "alice" }, attributes: { projectId: "123" } };
    const { tuple, error } = renderTuple(writeRule().tupleTemplate, e2);
    expect(error).toBeUndefined();
    expect(tuple).toEqual({ user: "user:alice", relation: "member", object: "team:123" });
  });

  test("rejects forbidden chars in substituted value (no userset/wildcard injection)", () => {
    const e2: IdpEvent = { type: "x", subject: { id: "alice" }, attributes: { projectId: "1#member" } };
    expect(renderTuple(writeRule().tupleTemplate, e2).error).toMatch(/forbidden/);
  });

  test("rejects unresolved placeholder", () => {
    const e2: IdpEvent = { type: "x", subject: { id: "alice" }, attributes: {} };
    expect(renderTuple(writeRule().tupleTemplate, e2).error).toMatch(/unresolved/);
  });

  test("requires literal type: prefix (event must not choose the type)", () => {
    const e2: IdpEvent = { type: "x", subject: { id: "alice" }, attributes: { t: "document", projectId: "123" } };
    // fully dynamic object
    expect(
      renderTuple({ user: "user:{{subject.id}}", relation: "member", object: "{{attributes.projectId}}" }, e2).error,
    ).toMatch(/literal type/);
    // split type {{t}}:{{projectId}} is also rejected
    expect(
      renderTuple(
        { user: "user:{{subject.id}}", relation: "member", object: "{{attributes.t}}:{{attributes.projectId}}" },
        e2,
      ).error,
    ).toMatch(/literal type/);
  });
});

function fakeDeps(write: ApplyDeps["writeTuple"]): { deps: ApplyDeps; audits: string[] } {
  const audits: string[] = [];
  return { deps: { writeTuple: write, audit: (a) => audits.push(a) }, audits };
}

describe("applyEvents", () => {
  test("applies matched rules; counts applied", async () => {
    const e2: IdpEvent = { type: "user.grant.added", subject: { id: "alice" }, attributes: { projectId: "123" } };
    const { deps } = fakeDeps(async () => "applied");
    expect(await applyEvents([e2], [writeRule()], deps)).toEqual({ applied: 1, skipped: 0, failed: 0 });
  });

  test("no matching rule → skipped", async () => {
    const e2: IdpEvent = { type: "unmatched", subject: { id: "alice" }, attributes: {} };
    const { deps } = fakeDeps(async () => "applied");
    expect(await applyEvents([e2], [writeRule()], deps)).toEqual({ applied: 0, skipped: 1, failed: 0 });
  });

  test("idempotent write (already exists) → skipped, not failed", async () => {
    const e2: IdpEvent = { type: "user.grant.added", subject: { id: "alice" }, attributes: { projectId: "123" } };
    const { deps } = fakeDeps(async () => "skipped");
    expect(await applyEvents([e2], [writeRule()], deps)).toEqual({ applied: 0, skipped: 1, failed: 0 });
  });

  test("render error → failed, continues", async () => {
    const e2: IdpEvent = { type: "user.grant.added", subject: { id: "alice" }, attributes: {} }; // no projectId
    const { deps } = fakeDeps(async () => "applied");
    expect(await applyEvents([e2], [writeRule()], deps)).toEqual({ applied: 0, skipped: 0, failed: 1 });
  });

  test("deterministic write error → failed, continues", async () => {
    const e2: IdpEvent = { type: "user.grant.added", subject: { id: "alice" }, attributes: { projectId: "123" } };
    const { deps } = fakeDeps(async () => {
      throw new WriteError(false, "type_not_found");
    });
    expect(await applyEvents([e2], [writeRule()], deps)).toEqual({ applied: 0, skipped: 0, failed: 1 });
  });

  test("transient write error → rethrows (→ 502)", async () => {
    const e2: IdpEvent = { type: "user.grant.added", subject: { id: "alice" }, attributes: { projectId: "123" } };
    const { deps } = fakeDeps(async () => {
      throw new WriteError(true, "fetch failed");
    });
    await expect(applyEvents([e2], [writeRule()], deps)).rejects.toBeInstanceOf(WriteError);
  });

  test("rules applied in priority order", async () => {
    const order: RenderedTuple[] = [];
    const { deps } = fakeDeps(async (_op, t) => {
      order.push(t);
      return "applied";
    });
    const e2: IdpEvent = { type: "user.grant.added", subject: { id: "alice" }, attributes: { projectId: "123" } };
    const r1 = writeRule({ priority: 2, tupleTemplate: { user: "user:a", relation: "r2", object: "team:1" } });
    const r2 = writeRule({ priority: 1, tupleTemplate: { user: "user:a", relation: "r1", object: "team:1" } });
    await applyEvents([e2], [r1, r2], deps);
    expect(order.map((t) => t.relation)).toEqual(["r1", "r2"]);
  });
});
