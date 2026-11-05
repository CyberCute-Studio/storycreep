import { describe, expect, it } from "vitest";
import { formatBytes } from "./Usage";

// The sizes that the workspace's usage shows, in the units of 1,024 that
// the server and the plans count in, so that a limit of a whole number of
// them shows as that number.

describe("formatBytes", () => {
  it("shows a size in the largest unit of 1,024 that it is at least one of", () => {
    expect(formatBytes("en", 512)).toBe("512 byte");
    expect(formatBytes("en", 1536)).toBe("1.5 kB");
    expect(formatBytes("en", 104857600)).toBe("100 MB");
    expect(formatBytes("en", 10737418240)).toBe("10 GB");
  });
});
