import { describe, expect, test } from "bun:test";
import { computeSignature, signatureHeader, zitadelAdapter, SIGNATURE_TOLERANCE_MS } from "./zitadel";

const enc = (o: unknown): Uint8Array => new TextEncoder().encode(JSON.stringify(o));
const SECRET = "topsecret";
const hdr = (sig: string): Headers => new Headers({ "ZITADEL-Signature": sig });

describe("zitadelAdapter.verifySignature", () => {
  const body = enc({ eventType: "user.grant.added" });

  test("accepts a freshly signed request", () => {
    const t = Date.now();
    expect(zitadelAdapter.verifySignature(body, hdr(signatureHeader(body, SECRET, t)), SECRET)).toBe(true);
  });

  test("rejects a tampered body", () => {
    const t = Date.now();
    const sig = signatureHeader(body, SECRET, t);
    const tampered = enc({ eventType: "user.grant.added", evil: true });
    expect(zitadelAdapter.verifySignature(tampered, hdr(sig), SECRET)).toBe(false);
  });

  test("rejects a wrong secret", () => {
    const t = Date.now();
    expect(zitadelAdapter.verifySignature(body, hdr(signatureHeader(body, SECRET, t)), "other")).toBe(false);
  });

  test("rejects a stale timestamp (replay)", () => {
    const stale = Date.now() - SIGNATURE_TOLERANCE_MS - 1000;
    expect(zitadelAdapter.verifySignature(body, hdr(signatureHeader(body, SECRET, stale)), SECRET)).toBe(false);
  });

  test("rejects a future-dated timestamp (both-sided window)", () => {
    const future = Date.now() + SIGNATURE_TOLERANCE_MS + 1000;
    expect(zitadelAdapter.verifySignature(body, hdr(signatureHeader(body, SECRET, future)), SECRET)).toBe(false);
  });

  test("rejects a missing/malformed header and empty/non-hex v1", () => {
    const t = Date.now();
    expect(zitadelAdapter.verifySignature(body, new Headers(), SECRET)).toBe(false);
    expect(zitadelAdapter.verifySignature(body, hdr("garbage"), SECRET)).toBe(false);
    expect(zitadelAdapter.verifySignature(body, hdr(`t=${t},v1=`), SECRET)).toBe(false);
    expect(zitadelAdapter.verifySignature(body, hdr(`t=${t},v1=zzzz`), SECRET)).toBe(false);
  });

  test("computeSignature is deterministic and case-insensitive on header name", () => {
    const t = 1782000000000;
    expect(computeSignature(body, SECRET, t)).toBe(computeSignature(body, SECRET, t));
    const lower = new Headers({ "zitadel-signature": signatureHeader(body, SECRET, Date.now()) });
    expect(zitadelAdapter.verifySignature(body, lower, SECRET)).toBe(true);
  });
});

describe("zitadelAdapter.parseEvents", () => {
  test("grant.added → one IdpEvent with projectId/grantId", () => {
    const events = zitadelAdapter.parseEvents(
      {
        eventType: "user.grant.added",
        aggregateID: "alice",
        payload: { projectID: "eng", grantID: "g1", userName: "alice@x" },
      },
      new Headers(),
    );
    expect(events).toEqual([
      {
        type: "user.grant.added",
        subject: { id: "alice" },
        attributes: { projectId: "eng", grantId: "g1", username: "alice@x" },
      },
    ]);
  });

  test("user.human.added → IdpEvent without grant attributes", () => {
    const events = zitadelAdapter.parseEvents(
      { eventType: "user.human.added", aggregateID: "bob", payload: { userName: "bob@x" } },
      new Headers(),
    );
    expect(events[0]).toEqual({ type: "user.human.added", subject: { id: "bob" }, attributes: { username: "bob@x" } });
  });

  test("malformed payload → [] (no crash)", () => {
    expect(zitadelAdapter.parseEvents({ nope: 1 }, new Headers())).toEqual([]);
    expect(zitadelAdapter.parseEvents("not-json-object", new Headers())).toEqual([]);
    expect(zitadelAdapter.parseEvents([1, 2, 3], new Headers())).toEqual([]);
    expect(zitadelAdapter.parseEvents(42, new Headers())).toEqual([]);
    expect(zitadelAdapter.parseEvents(null, new Headers())).toEqual([]);
  });
});
