import { createHash } from "node:crypto";
import { expect, test } from "vitest";
import { sha256 } from "./sha256";

const hex = (b: Uint8Array) =>
  Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");

// bytes returns n bytes of no pattern that a block's boundary hides.
function bytes(n: number): Uint8Array {
  return Uint8Array.from({ length: n }, (_, i) => (i * 131 + (i >> 8)) & 255);
}

// The examples of FIPS 180-2, and the empty message.
test("sha256 hashes the standard's examples", () => {
  const text = (s: string) => hex(sha256(new TextEncoder().encode(s)));
  expect(text("")).toBe(
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  );
  expect(text("abc")).toBe(
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  );
  expect(text("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")).toBe(
    "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
  );
  expect(hex(sha256(new Uint8Array(1_000_000).fill(0x61)))).toBe(
    "cdc76e5c9914fb9281a1c7e284d73e67f1809a48a497200e046d39ccc7112cd0",
  );
});

// Every length of a message across the boundaries of three blocks, where
// its length fits in its last block or does not, and a view of a part of a
// larger buffer, which it hashes alone.
test("sha256 is what node:crypto gives of a message of any length", () => {
  for (let n = 0; n <= 200; n++) {
    const b = bytes(n);
    expect(hex(sha256(b)), String(n)).toBe(
      createHash("sha256").update(b).digest("hex"),
    );
  }
  const part = bytes(100_000).subarray(13, 70_000);
  expect(hex(sha256(part))).toBe(
    createHash("sha256").update(part).digest("hex"),
  );
});
