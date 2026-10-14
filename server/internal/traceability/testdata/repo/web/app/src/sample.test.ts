import { expect, it, test } from "vitest";

// Verifies: FR-A-02/AC1, NFR-Q-02
test("a vitest test", () => {
  expect(1).toBe(1);
});

// Verifies: FR-A-02/AC2
it.skip("a skipped test", () => {
  expect(1).toBe(2);
});
