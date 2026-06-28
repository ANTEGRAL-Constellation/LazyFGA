import type { ModelIR } from "@lazyfga/shared";
import { docFolderTeamIR } from "@lazyfga/shared/fixtures";
import { describe, expect, test } from "bun:test";
import { explain, type ReasonDeps } from "./reason";

const ir: ModelIR = docFolderTeamIR;
const pin = (decision: boolean) => ({ decision, authorizationModelId: "m1", ir });

/** 설정 가능한 가짜 OpenFGA(테스트용). */
function fakeDeps(opts: {
  allow: (rel: string, obj: string) => boolean;
  tuples?: (obj: string, rel: string) => string[];
}): ReasonDeps {
  return {
    check: async ({ relation, object }) => ({ allowed: opts.allow(relation, object) }),
    read: async ({ object, relation }) => ({
      tuples: (opts.tuples?.(object ?? "", relation ?? "") ?? []).map((u) => ({
        user: u,
        relation: relation ?? "",
        object: object ?? "",
      })),
    }),
  };
}

describe("explain — allow witness", () => {
  test("direct role", async () => {
    const deps = fakeDeps({
      allow: (rel, obj) => rel === "viewer" && obj === "document:123",
      tuples: (obj, rel) => (obj === "document:123" && rel === "viewer" ? ["user:alice"] : []),
    });
    const r = await explain("user:alice", "read", "document:123", undefined, pin(true), deps);
    expect(r.decision).toBe(true);
    expect(r.truncated).toBeFalsy();
    expect(r.path).toEqual([{ via: "role", role: "viewer", on: "document", direct: true }]);
  });

  test("via group membership (verified by Check)", async () => {
    const deps = fakeDeps({
      allow: (rel, obj) =>
        (rel === "viewer" && obj === "document:123") || (rel === "member" && obj === "team:eng"),
      tuples: (obj, rel) =>
        obj === "document:123" && rel === "viewer" ? ["team:eng#member"] : [],
    });
    const r = await explain("user:alice", "read", "document:123", undefined, pin(true), deps);
    expect(r.truncated).toBeFalsy();
    expect(r.path?.[0]).toEqual({
      via: "role",
      role: "viewer",
      on: "document",
      direct: false,
      group: "team",
      groupObject: "team:eng",
    });
  });

  test("unverifiable grant (wildcard) → truncated, honest", async () => {
    const deps = fakeDeps({
      allow: (rel, obj) => rel === "viewer" && obj === "document:123",
      tuples: (obj, rel) => (obj === "document:123" && rel === "viewer" ? ["user:*"] : []),
    });
    const r = await explain("user:alice", "read", "document:123", undefined, pin(true), deps);
    expect(r.decision).toBe(true);
    expect(r.truncated).toBe(true);
  });

  test("inherited via parent (recursion) keeps concrete object id", async () => {
    const deps = fakeDeps({
      allow: (rel, obj) => rel === "viewer" && obj === "folder:1",
      tuples: (obj, rel) => {
        if (obj === "document:123" && rel === "parent") return ["folder:1"];
        if (obj === "folder:1" && rel === "viewer") return ["user:alice"];
        return [];
      },
    });
    const r = await explain("user:alice", "read", "document:123", undefined, pin(true), deps);
    expect(r.truncated).toBeFalsy();
    expect(r.path).toEqual([
      { via: "parent", relation: "parent", parent: "folder", parentObject: "folder:1" },
      { via: "role", role: "viewer", on: "folder", direct: true },
    ]);
  });
});

describe("explain — deny", () => {
  test("missing links from IR", async () => {
    const deps = fakeDeps({ allow: () => false });
    const r = await explain("user:bob", "read", "document:123", undefined, pin(false), deps);
    expect(r.decision).toBe(false);
    expect(r.missingLinks).toContainEqual({
      kind: "role",
      anyOf: ["viewer", "editor", "owner"],
      on: "document",
    });
    expect(r.missingLinks).toContainEqual({ kind: "parent", relation: "parent", needs: "can_read" });
  });
});

describe("explain — termination", () => {
  test("self-referential parent cycle terminates (visited guard)", async () => {
    const deps = fakeDeps({
      allow: () => false, // no role grant anywhere
      tuples: (obj, rel) => (rel === "parent" && obj === "document:123" ? ["document:123"] : []),
    });
    const r = await explain("user:alice", "read", "document:123", undefined, pin(true), deps);
    // decision=true(상위 Check) but witness 재구성 불가 → 정직한 truncated fallback, 무한루프 없음.
    expect(r.decision).toBe(true);
    expect(r.truncated).toBe(true);
    expect(r.path).toBeUndefined();
  });
});
