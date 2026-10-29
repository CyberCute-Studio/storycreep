import { createHash } from "node:crypto";
import { afterEach, describe, expect, test, vi } from "vitest";
import { sha256, sha256Yielding, YIELD_EVERY } from "./sha256";

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

// A message of more bytes than sha256Yielding hashes at a time, of fewer, and
// of a whole number of times as many, with a part of a block left or none.
test("sha256Yielding is what sha256 gives", async () => {
  for (const n of [
    0,
    100,
    YIELD_EVERY - 1,
    YIELD_EVERY,
    YIELD_EVERY + 1,
    2 * YIELD_EVERY + 63,
    3 * YIELD_EVERY,
  ]) {
    const b = bytes(n);
    expect(hex(await sha256Yielding(b)), String(n)).toBe(hex(sha256(b)));
  }
});

// A page goes on running tasks while it hashes a large file, which it hashes
// as it was when it was given, whatever becomes of it meanwhile.
test("sha256Yielding lets the page run, and hashes the bytes as they were", async () => {
  const b = bytes(4 * YIELD_EVERY + 5);
  const want = hex(sha256(b));
  let ran = 0;
  const tick = () => {
    ran++;
    if (ran < 3) {
      setTimeout(tick, 0);
    }
  };
  setTimeout(tick, 0);
  const sum = sha256Yielding(b);
  b.fill(0);
  expect(hex(await sum)).toBe(want);
  expect(ran).toBeGreaterThan(0);
});

// A browser delays a timer nested five deep by 4 ms, and throttles a hidden
// tab's timers to once a second, or once a minute, so sha256Yielding lets
// the page run by a message rather than by a timer, and hashes as soon
// whatever becomes of the timers.
describe("without a timer", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  test("sha256Yielding lets the page run", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "setInterval", "setImmediate"] });
    const b = bytes(3 * YIELD_EVERY + 5);
    expect(hex(await sha256Yielding(b))).toBe(hex(sha256(b)));
  });
});

// Once its signal is aborted, sha256Yielding rejects with its reason before
// it hashes another part of the bytes, as load does once its own is.
test("sha256Yielding stops once its signal is aborted", async () => {
  const c = new AbortController();
  const sum = sha256Yielding(bytes(3 * YIELD_EVERY), c.signal);
  c.abort(new Error("stopped"));
  await expect(sum).rejects.toThrow("stopped");
});
