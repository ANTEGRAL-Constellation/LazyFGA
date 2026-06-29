import { describe, expect, test } from "bun:test";
import { attributeNamesForEvent, extractEvent, readEventType } from "./extraction";
import type { ProviderPreset } from "./extraction";
import { PRESETS } from "./presets";

const Z = PRESETS.zitadel!;

describe("extractEvent — ZITADEL preset", () => {
  test("signup (user-aggregate) → subject from aggregateID, org from resourceOwner", () => {
    const ev = extractEvent(Z, {
      event_type: "user.human.added",
      aggregateID: "bob",
      resourceOwner: "org1",
      event_payload: { userName: "bob@x" },
    });
    expect(ev).toEqual({ type: "user.human.added", subject: { type: "user", id: "bob" }, attributes: { org: "org1" } });
  });

  test("self-registered also matches the signup rule", () => {
    const ev = extractEvent(Z, { event_type: "user.human.selfregistered", aggregateID: "carol", resourceOwner: "org2" });
    expect(ev?.subject.id).toBe("carol");
  });

  test("grant.added (usergrant-aggregate) → subject from event_payload.userId, project from event_payload.projectId", () => {
    const ev = extractEvent(Z, {
      event_type: "user.grant.added",
      aggregateID: "grant-99", // grant id, NOT the user — must be ignored
      event_payload: { userId: "alice", projectId: "eng", roleKeys: ["editor", "viewer"] },
    });
    expect(ev).toEqual({
      type: "user.grant.added",
      subject: { type: "user", id: "alice" },
      attributes: { project: "eng", roleKeys: ["editor", "viewer"] },
    });
  });

  test("grant.removed → no roleKeys attribute (event carries none)", () => {
    const ev = extractEvent(Z, {
      event_type: "user.grant.removed",
      aggregateID: "grant-99",
      event_payload: { userId: "alice", projectId: "eng" },
    });
    expect(ev).toEqual({ type: "user.grant.removed", subject: { type: "user", id: "alice" }, attributes: { project: "eng" } });
  });

  test("unmatched event type → null (ignored, not every event maps)", () => {
    expect(extractEvent(Z, { event_type: "session.created", aggregateID: "x" })).toBeNull();
  });

  test("matched event but missing/empty/non-string subject → null (not coerced)", () => {
    expect(extractEvent(Z, { event_type: "user.grant.added", event_payload: { projectId: "eng" } })).toBeNull(); // no userId
    expect(extractEvent(Z, { event_type: "user.grant.added", event_payload: { userId: "", projectId: "eng" } })).toBeNull();
    expect(extractEvent(Z, { event_type: "user.grant.added", event_payload: { userId: 123, projectId: "eng" } })).toBeNull();
  });

  test("numeric scalar attribute is coerced to string", () => {
    const ev = extractEvent(Z, { event_type: "user.grant.added", event_payload: { userId: "alice", projectId: 42 } });
    expect(ev?.attributes.project).toBe("42");
  });

  test("malformed / non-object body → null (no crash)", () => {
    expect(extractEvent(Z, null)).toBeNull();
    expect(extractEvent(Z, 42)).toBeNull();
    expect(extractEvent(Z, "nope")).toBeNull();
    expect(extractEvent(Z, { nope: 1 })).toBeNull(); // no event_type
  });

  test("array of non-scalars is filtered to scalar elements only", () => {
    const ev = extractEvent(Z, {
      event_type: "user.grant.added",
      event_payload: { userId: "alice", projectId: "eng", roleKeys: ["editor", { x: 1 }, 7] },
    });
    expect(ev?.attributes.roleKeys).toEqual(["editor", "7"]);
  });
});

describe("extractEvent — Standard Webhooks preset", () => {
  const SW = PRESETS["standard-webhooks"]!;
  test("user.created → subject from data.id, org from data.orgId", () => {
    const ev = extractEvent(SW, { type: "user.created", data: { id: "u1", orgId: "acme" } });
    expect(ev).toEqual({ type: "user.created", subject: { type: "user", id: "u1" }, attributes: { org: "acme" } });
  });
});

describe("getPath prototype-pollution guard (review)", () => {
  // a preset that tries to read dangerous segments must yield no value (never the prototype/constructor).
  const evil: ProviderPreset = {
    signature: Z.signature,
    typePath: "type",
    extraction: [
      { match: ["x"], subjectType: "user", subjectIdPath: "__proto__.polluted", attributePaths: { c: "constructor.name" } },
    ],
  };
  test("__proto__ / constructor path segments resolve to nothing → null subject", () => {
    // even if Object.prototype had a 'polluted' key, the guard must not read it.
    expect(extractEvent(evil, { type: "x", a: 1 })).toBeNull();
  });
  test("does not read inherited (non-own) properties", () => {
    const p: ProviderPreset = {
      signature: Z.signature,
      typePath: "type",
      extraction: [{ match: ["x"], subjectType: "user", subjectIdPath: "toString", attributePaths: {} }],
    };
    expect(extractEvent(p, { type: "x" })).toBeNull(); // toString is inherited, not own → undefined
  });
});

describe("readEventType / attributeNamesForEvent (review: fanOut validation support)", () => {
  test("readEventType reads the type via the preset typePath", () => {
    expect(readEventType(Z, { event_type: "user.grant.added" })).toBe("user.grant.added");
    expect(readEventType(Z, { nope: 1 })).toBeUndefined();
    expect(readEventType(Z, null)).toBeUndefined();
  });
  test("attributeNamesForEvent returns the produced attribute names for an event type", () => {
    expect([...attributeNamesForEvent(Z, "user.grant.added")].sort()).toEqual(["project", "roleKeys"]);
    expect([...attributeNamesForEvent(Z, "user.grant.removed")]).toEqual(["project"]);
    expect([...attributeNamesForEvent(Z, "user.human.added")]).toEqual(["org"]);
    expect([...attributeNamesForEvent(Z, "unknown")]).toEqual([]);
  });
});
