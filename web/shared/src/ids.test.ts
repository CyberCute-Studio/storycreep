import { describe, expect, it } from "vitest";
import rawVectors from "../../../schema/ids/vectors.json" with { type: "json" };
import {
  decodeAnyId,
  decodeId,
  encodeId,
  formatUuid,
  ID_PREFIXES,
  IdDecodeError,
  type IdKind,
  newId,
  newUuid,
  parseUuid,
  uuidV7From,
} from "./ids";

// schema/ids/vectors.json, shared with the Go implementation. See
// schema/ids/README.md.
interface Vectors {
  prefixes: Record<string, string>;
  encode: { note?: string; kind: IdKind; uuid: string; id: string }[];
  decode: {
    note: string;
    kind: IdKind;
    id: string;
    uuid: string;
    canonical: string;
  }[];
  decode_errors: {
    note: string;
    kind: IdKind;
    id: string;
    error: string;
    got?: IdKind;
  }[];
  uuidv7: { note: string; unix_ms: number; random: string; uuid: string }[];
}

const vectors = rawVectors as Vectors;

function hexBytes(hex: string): Uint8Array {
  return Uint8Array.from(hex.match(/../g) ?? [], (byte) =>
    Number.parseInt(byte, 16),
  );
}

function catchDecodeError(f: () => unknown): IdDecodeError {
  try {
    f();
  } catch (error) {
    if (error instanceof IdDecodeError) {
      return error;
    }
    throw error;
  }
  throw new Error("decoding succeeded, want an IdDecodeError");
}

describe("the shared vectors", () => {
  it("have no empty section", () => {
    expect(Object.keys(vectors.prefixes)).not.toHaveLength(0);
    for (const section of [
      vectors.encode,
      vectors.decode,
      vectors.decode_errors,
      vectors.uuidv7,
    ]) {
      expect(section).not.toHaveLength(0);
    }
  });

  it("list exactly the prefixes of ID_PREFIXES", () => {
    expect(ID_PREFIXES).toStrictEqual(vectors.prefixes);
  });

  it.each(vectors.encode)(
    "encode $kind $uuid ($note)",
    ({ kind, uuid, id }) => {
      const bytes = parseUuid(uuid);
      expect(formatUuid(bytes)).toBe(uuid);
      expect(encodeId(kind, bytes)).toBe(id);
      expect(decodeId(kind, id)).toStrictEqual(bytes);
      expect(decodeAnyId(id)).toStrictEqual({ kind, uuid: bytes });
    },
  );

  it.each(vectors.decode)(
    "decode $id ($note)",
    ({ kind, id, uuid, canonical }) => {
      const bytes = parseUuid(uuid);
      expect(decodeId(kind, id)).toStrictEqual(bytes);
      expect(decodeAnyId(id)).toStrictEqual({ kind, uuid: bytes });
      expect(encodeId(kind, bytes)).toBe(canonical);
    },
  );

  // A typed error is what the API maps to 400 (plan item 6).
  it.each(vectors.decode_errors)(
    "reject $id as $kind: $error ($note)",
    ({ kind, id, error, got }) => {
      const err = catchDecodeError(() => decodeId(kind, id));
      expect({
        reason: err.reason,
        expected: err.expected,
        got: err.got,
      }).toStrictEqual({
        reason: error,
        expected: kind,
        got,
      });

      if (error === "wrong_prefix" && got !== undefined) {
        // Without an expected type, the input is just an identifier of the
        // other type, valid or not.
        let want: unknown;
        try {
          want = { kind: got, uuid: decodeId(got, id) };
        } catch (wantErr) {
          const anyErr = catchDecodeError(() => decodeAnyId(id));
          expect(anyErr.reason).toBe((wantErr as IdDecodeError).reason);
          expect(anyErr.got).toBe(got);
          return;
        }
        expect(decodeAnyId(id)).toStrictEqual(want);
        return;
      }
      const anyErr = catchDecodeError(() => decodeAnyId(id));
      expect({
        reason: anyErr.reason,
        expected: anyErr.expected,
        got: anyErr.got,
      }).toStrictEqual({
        reason: error,
        expected: undefined,
        got,
      });
    },
  );

  it.each(vectors.uuidv7)(
    "build a UUIDv7 from $unix_ms and $random ($note)",
    ({ unix_ms, random, uuid }) => {
      expect(formatUuid(uuidV7From(unix_ms, hexBytes(random)))).toBe(uuid);
    },
  );
});

describe("newUuid", () => {
  it("makes a UUIDv7 for the current time", () => {
    const before = Date.now();
    const uuid = newUuid();
    const after = Date.now();
    expect(uuid[6] >> 4).toBe(7);
    expect(uuid[8] >> 6).toBe(0b10);
    const ms = uuid.slice(0, 6).reduce((acc, byte) => acc * 256 + byte, 0);
    expect(ms).toBeGreaterThanOrEqual(before);
    expect(ms).toBeLessThanOrEqual(after);
  });

  it("does not repeat", () => {
    const seen = new Set<string>();
    for (let i = 0; i < 1000; i++) {
      seen.add(formatUuid(newUuid()));
    }
    expect(seen.size).toBe(1000);
  });
});

describe("newId", () => {
  it("encodes a new UUID with the type's prefix", () => {
    const id = newId("branch_output");
    expect(id).toMatch(/^bo_[0-7][0-9A-HJKMNP-TV-Z]{25}$/);
    expect(decodeId("branch_output", id)[6] >> 4).toBe(7);
  });
});

describe("encodeId", () => {
  // Encoded identifiers of one type sort in the same order as their UUIDs, so
  // UUIDv7 identifiers sort by time as text.
  it("sorts like the UUIDs", () => {
    let seed = 1;
    const random = () => {
      // A small linear congruential generator, so a failure reproduces.
      seed = (seed * 1103515245 + 12345) % 2 ** 31;
      return seed;
    };
    const compareBytes = (a: Uint8Array, b: Uint8Array) => {
      for (let i = 0; i < a.length; i++) {
        if (a[i] !== b[i]) {
          return a[i] < b[i] ? -1 : 1;
        }
      }
      return 0;
    };
    // Few distinct byte values make long shared prefixes, which reach the low bits.
    const uuid = () =>
      Uint8Array.from({ length: 16 }, () => (random() % 3) * 0x7f);
    for (let i = 0; i < 10000; i++) {
      const a = uuid();
      const b = uuid();
      const ea = encodeId("node", a);
      const eb = encodeId("node", b);
      expect(
        Math.sign(ea < eb ? -1 : ea > eb ? 1 : 0),
        `${formatUuid(a)} vs ${formatUuid(b)}`,
      ).toBe(compareBytes(a, b));
    }
    const early = encodeId(
      "node",
      uuidV7From(1_700_000_000_000, new Uint8Array(10).fill(0xff)),
    );
    const late = encodeId(
      "node",
      uuidV7From(1_700_000_000_001, new Uint8Array(10)),
    );
    expect(early < late).toBe(true);
  });

  it("rejects a value that is not 16 bytes", () => {
    expect(() => encodeId("node", new Uint8Array(15))).toThrow(RangeError);
    expect(() => encodeId("node", new Uint8Array(17))).toThrow(RangeError);
  });

  it("rejects a type that is not an entity type", () => {
    expect(() => encodeId("toString" as IdKind, new Uint8Array(16))).toThrow(
      RangeError,
    );
  });
});

describe("decodeId", () => {
  it("throws an error that names both types", () => {
    const err = catchDecodeError(() =>
      decodeId("board", "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD"),
    );
    expect(err).toBeInstanceOf(Error);
    expect(err.name).toBe("IdDecodeError");
    expect(err.message).toBe(
      "invalid identifier for type board: its prefix nd_ is for type node",
    );
  });

  it("does not accept a property of Object.prototype as a prefix", () => {
    expect(
      catchDecodeError(() =>
        decodeAnyId("constructor_01HQ8Z3XK4F7NAWR5TCQ9M2BVD"),
      ).reason,
    ).toBe("unknown_prefix");
  });
});

describe("uuidV7From", () => {
  it.each([-1, 2 ** 48, 0.5, Number.NaN])("rejects the time %d", (ms) => {
    expect(() => uuidV7From(ms, new Uint8Array(10))).toThrow(RangeError);
  });

  it("needs exactly 10 random bytes", () => {
    expect(() => uuidV7From(0, new Uint8Array(9))).toThrow(RangeError);
    expect(() => uuidV7From(0, new Uint8Array(11))).toThrow(RangeError);
  });
});

describe("parseUuid", () => {
  it.each([
    "",
    "017f22e279b07cc398c4dc0c0c07398f",
    "{017f22e2-79b0-7cc3-98c4-dc0c0c07398f}",
    "urn:uuid:017f22e2-79b0-7cc3-98c4-dc0c0c07398f",
    "017f22e2-79b0-7cc3-98c4-dc0c0c07398",
    "017f22e2-79b0-7cc3-98c4-dc0c0c07398f0",
    "017f22e2_79b0-7cc3-98c4-dc0c0c07398f",
    "017f22e2-79b07-cc3-98c4-dc0c0c07398f",
    "017f22e2-79b0-7cc3-98c4-dc0c0c07398g",
    "017f22e2-79b0-7cc3-98c4-dc0c0c0739+f",
    "017f22e2-79b0-7cc3-98c4-dc0c0c07398f\n",
  ])("rejects %j", (text) => {
    expect(() => parseUuid(text)).toThrow(SyntaxError);
  });

  it("accepts uppercase digits", () => {
    expect(formatUuid(parseUuid("017F22E2-79B0-7CC3-98C4-DC0C0C07398F"))).toBe(
      "017f22e2-79b0-7cc3-98c4-dc0c0c07398f",
    );
  });
});
