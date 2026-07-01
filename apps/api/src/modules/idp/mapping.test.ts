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
  subject: { type: "user", id: "alice" },
  attributes: { project: "123", role: "editor" },
};

const writeRule = (over: Partial<MappingRule> = {}): MappingRule => ({
  eventType: "user.grant.added",
  match: [],
  tupleTemplate: {
    user: "user:{{subject}}",
    relation: "member",
    object: "team:{{attributes.project}}",
  },
  op: "write",
  priority: 0,
  ...over,
});

describe("matchRule", () => {
  test("matches on eventType + predicates", () => {
    expect(
      matchRule(writeRule({ match: [{ field: "attributes.role", equals: "editor" }] }), ev),
    ).toBe(true);
    expect(
      matchRule(writeRule({ match: [{ field: "attributes.role", equals: "viewer" }] }), ev),
    ).toBe(false);
    expect(matchRule(writeRule({ eventType: "other" }), ev)).toBe(false);
  });

  test("subject placeholder field matches", () => {
    expect(matchRule(writeRule({ match: [{ field: "subject", equals: "alice" }] }), ev)).toBe(true);
  });

  test("fan-out rule only matches when the array attribute is non-empty", () => {
    const withRoles: IdpEvent = { ...ev, attributes: { project: "123", roleKeys: ["a", "b"] } };
    const empty: IdpEvent = { ...ev, attributes: { project: "123", roleKeys: [] } };
    const fr = writeRule({
      fanOut: "roleKeys",
      tupleTemplate: {
        user: "user:{{subject}}",
        relation: "{{item}}",
        object: "team:{{attributes.project}}",
      },
    });
    expect(matchRule(fr, withRoles)).toBe(true);
    expect(matchRule(fr, empty)).toBe(false);
  });
});

describe("renderTuple (injection guard)", () => {
  test("substitutes placeholders into a valid tuple (subject + subject.id alias)", () => {
    const e2: IdpEvent = {
      type: "x",
      subject: { type: "user", id: "alice" },
      attributes: { project: "123" },
    };
    expect(renderTuple(writeRule().tupleTemplate, e2).tuple).toEqual({
      user: "user:alice",
      relation: "member",
      object: "team:123",
    });
    // {{subject.id}} alias resolves to the same id.
    expect(
      renderTuple(
        { user: "user:{{subject.id}}", relation: "member", object: "team:{{attributes.project}}" },
        e2,
      ).tuple,
    ).toEqual({ user: "user:alice", relation: "member", object: "team:123" });
  });

  test("rejects forbidden chars in substituted value (no userset/wildcard injection)", () => {
    const e2: IdpEvent = {
      type: "x",
      subject: { type: "user", id: "alice" },
      attributes: { project: "1#member" },
    };
    expect(renderTuple(writeRule().tupleTemplate, e2).error).toMatch(/forbidden/);
  });

  test("rejects unresolved placeholder", () => {
    const e2: IdpEvent = { type: "x", subject: { type: "user", id: "alice" }, attributes: {} };
    expect(renderTuple(writeRule().tupleTemplate, e2).error).toMatch(/unresolved/);
  });

  test("array attribute in a scalar slot errors (must use fan-out)", () => {
    const e2: IdpEvent = {
      type: "x",
      subject: { type: "user", id: "alice" },
      attributes: { project: ["a", "b"] },
    };
    expect(renderTuple(writeRule().tupleTemplate, e2).error).toMatch(/array|fan-out/);
  });

  test("{{item}} without a provided item errors", () => {
    const e2: IdpEvent = {
      type: "x",
      subject: { type: "user", id: "alice" },
      attributes: { project: "1" },
    };
    expect(
      renderTuple(
        { user: "user:{{subject}}", relation: "{{item}}", object: "team:{{attributes.project}}" },
        e2,
      ).error,
    ).toMatch(/item/);
  });

  test("{{item}} binds the fan-out element", () => {
    const e2: IdpEvent = {
      type: "x",
      subject: { type: "user", id: "alice" },
      attributes: { project: "1" },
    };
    expect(
      renderTuple(
        { user: "user:{{subject}}", relation: "{{item}}", object: "team:{{attributes.project}}" },
        e2,
        "editor",
      ).tuple,
    ).toEqual({ user: "user:alice", relation: "editor", object: "team:1" });
  });

  test("requires literal type: prefix (event must not choose the type)", () => {
    const e2: IdpEvent = {
      type: "x",
      subject: { type: "user", id: "alice" },
      attributes: { t: "document", project: "123" },
    };
    expect(
      renderTuple(
        { user: "user:{{subject}}", relation: "member", object: "{{attributes.project}}" },
        e2,
      ).error,
    ).toMatch(/literal type/);
    expect(
      renderTuple(
        {
          user: "user:{{subject}}",
          relation: "member",
          object: "{{attributes.t}}:{{attributes.project}}",
        },
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
    const { deps } = fakeDeps(async () => "applied");
    expect(await applyEvents([ev], [writeRule()], deps)).toEqual({
      applied: 1,
      skipped: 0,
      failed: 0,
    });
  });

  test("no matching rule → skipped", async () => {
    const e2: IdpEvent = {
      type: "unmatched",
      subject: { type: "user", id: "alice" },
      attributes: {},
    };
    const { deps } = fakeDeps(async () => "applied");
    expect(await applyEvents([e2], [writeRule()], deps)).toEqual({
      applied: 0,
      skipped: 1,
      failed: 0,
    });
  });

  test("idempotent write (already exists) → skipped, not failed", async () => {
    const { deps } = fakeDeps(async () => "skipped");
    expect(await applyEvents([ev], [writeRule()], deps)).toEqual({
      applied: 0,
      skipped: 1,
      failed: 0,
    });
  });

  test("render error → failed, continues", async () => {
    const e2: IdpEvent = {
      type: "user.grant.added",
      subject: { type: "user", id: "alice" },
      attributes: {},
    };
    const { deps } = fakeDeps(async () => "applied");
    expect(await applyEvents([e2], [writeRule()], deps)).toEqual({
      applied: 0,
      skipped: 0,
      failed: 1,
    });
  });

  test("deterministic write error → failed, continues", async () => {
    const { deps } = fakeDeps(async () => {
      throw new WriteError(false, "type_not_found");
    });
    expect(await applyEvents([ev], [writeRule()], deps)).toEqual({
      applied: 0,
      skipped: 0,
      failed: 1,
    });
  });

  test("transient write error → rethrows (→ 502)", async () => {
    const { deps } = fakeDeps(async () => {
      throw new WriteError(true, "fetch failed");
    });
    await expect(applyEvents([ev], [writeRule()], deps)).rejects.toBeInstanceOf(WriteError);
  });

  test("rules applied in priority order", async () => {
    const order: RenderedTuple[] = [];
    const { deps } = fakeDeps(async (_op, t) => {
      order.push(t);
      return "applied";
    });
    const r1 = writeRule({
      priority: 2,
      tupleTemplate: { user: "user:a", relation: "r2", object: "team:1" },
    });
    const r2 = writeRule({
      priority: 1,
      tupleTemplate: { user: "user:a", relation: "r1", object: "team:1" },
    });
    await applyEvents([ev], [r1, r2], deps);
    expect(order.map((t) => t.relation)).toEqual(["r1", "r2"]);
  });

  describe("array fan-out", () => {
    const fanRule = (): MappingRule =>
      writeRule({
        fanOut: "roleKeys",
        tupleTemplate: {
          user: "user:{{subject}}",
          relation: "{{item}}",
          object: "team:{{attributes.project}}",
        },
      });

    test("emits one tuple per array element", async () => {
      const e2: IdpEvent = {
        type: "user.grant.added",
        subject: { type: "user", id: "alice" },
        attributes: { project: "1", roleKeys: ["editor", "viewer"] },
      };
      const tuples: RenderedTuple[] = [];
      const { deps } = fakeDeps(async (_op, t) => {
        tuples.push(t);
        return "applied";
      });
      expect(await applyEvents([e2], [fanRule()], deps)).toEqual({
        applied: 2,
        skipped: 0,
        failed: 0,
      });
      expect(tuples.map((t) => t.relation).sort()).toEqual(["editor", "viewer"]);
    });

    test("a forbidden-char element fails only that element; the rest apply", async () => {
      const e2: IdpEvent = {
        type: "user.grant.added",
        subject: { type: "user", id: "alice" },
        attributes: { project: "1", roleKeys: ["editor", "bad role"] },
      };
      const { deps } = fakeDeps(async () => "applied");
      expect(await applyEvents([e2], [fanRule()], deps)).toEqual({
        applied: 1,
        skipped: 0,
        failed: 1,
      });
    });

    test("empty array → rule does not match → skipped (no tuples)", async () => {
      const e2: IdpEvent = {
        type: "user.grant.added",
        subject: { type: "user", id: "alice" },
        attributes: { project: "1", roleKeys: [] },
      };
      const { deps } = fakeDeps(async () => "applied");
      expect(await applyEvents([e2], [fanRule()], deps)).toEqual({
        applied: 0,
        skipped: 1,
        failed: 0,
      });
    });
  });
});
