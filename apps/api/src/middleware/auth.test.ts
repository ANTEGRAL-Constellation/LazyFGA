import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { generateToken } from "./auth";

describe("generateToken", () => {
  test("plain token + matching sha256 hash, plain never stored", () => {
    const { plain, hash } = generateToken();
    expect(plain.length).toBeGreaterThan(20);
    expect(hash).toBe(createHash("sha256").update(plain).digest("hex"));
    expect(hash).toHaveLength(64);
  });

  test("tokens are unique", () => {
    expect(generateToken().plain).not.toBe(generateToken().plain);
  });
});
