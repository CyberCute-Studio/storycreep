import { describe, expect, it } from "vitest";
import {
  compareChildren,
  isKey,
  keyAfterSibling,
  keyBeforeSibling,
  keyBetween,
  maxKeyLength,
} from "./orderkey";

describe("keyBeforeSibling and keyAfterSibling", () => {
  const siblings = [
    { order: "V" },
    { order: "X" },
    { order: "X" },
    { order: "Z" },
  ];

  it("pick a key between a child and the one before or after it", () => {
    expect(keyBeforeSibling(siblings, 0)).toBe(keyBetween(undefined, "V"));
    expect(keyAfterSibling(siblings, 3)).toBe(keyBetween("Z", undefined));
    expect(keyAfterSibling(siblings, 0)).toBe(keyBetween("V", "X"));
  });

  it("go past the children that share the key of the one that they place by", () => {
    expect(keyBeforeSibling(siblings, 2)).toBe(keyBetween("V", "X"));
    expect(keyAfterSibling(siblings, 1)).toBe(keyBetween("X", "Z"));
  });
});

describe("keyBetween", () => {
  it("picks the keys that the server's documentation names", () => {
    expect(keyBetween(undefined, undefined)).toBe("V");
    expect(keyBetween("7", "9")).toBe("8");
    expect(keyBetween("7", "8")).toBe("7V");
  });

  it("picks a key before the first and after the last", () => {
    expect(keyBetween(undefined, "V")).toBe("G");
    expect(keyBetween("V", undefined)).toBe("l");
    expect(keyBetween(undefined, "1")).toBe("0V");
    expect(keyBetween("z", undefined)).toBe("zV");
  });

  it("picks a key between keys that share their first digits", () => {
    expect(keyBetween("V1", "V3")).toBe("V2");
    expect(keyBetween("79", "8A")).toBe("8");
    expect(keyBetween("7z", "8")).toBe("7zV");
  });

  it("always picks a key strictly between, which the server takes", () => {
    // Each key between the last one and the next, as a child dropped again
    // and again at one place.
    let low = "V";
    const high = "W";
    for (let i = 0; i < 200; i++) {
      const key = keyBetween(low, high) ?? "";
      expect(isKey(key)).toBe(true);
      expect(key > low).toBe(true);
      expect(key < high).toBe(true);
      low = key;
    }
  });

  it("has none longer than the server takes", () => {
    const shared = `V${"0".repeat(maxKeyLength - 2)}`;
    expect(keyBetween(`${shared}1`, `${shared}2`)).toBeUndefined();
  });

  it("has no key between two that are the same, nor between keys out of order", () => {
    expect(keyBetween("V", "V")).toBeUndefined();
    expect(keyBetween("W", "V")).toBeUndefined();
    expect(keyBetween("V0", "W")).toBeUndefined();
  });
});

describe("compareChildren", () => {
  it("orders by key, and children with the same key by their IDs without the prefix", () => {
    const children = [
      { id: "bd_02", order: "V" },
      { id: "fld_01", order: "V" },
      { id: "bd_00", order: "W" },
      { id: "fld_09", order: "U" },
    ];
    expect(children.sort(compareChildren).map((c) => c.id)).toEqual([
      "fld_09",
      "fld_01",
      "bd_02",
      "bd_00",
    ]);
  });
});
