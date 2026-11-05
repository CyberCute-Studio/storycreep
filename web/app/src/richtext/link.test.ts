import { describe, expect, test } from "vitest";
import { storedHref, typedHref } from "./link";

describe("a link's URL", () => {
  test("typed without a scheme is an https URL, as a host with a port is", () => {
    expect(typedHref("example.com/x")).toBe("https://example.com/x");
    expect(typedHref("example.com:8080/x")).toBe("https://example.com:8080/x");
    expect(typedHref("localhost:3000")).toBe("https://localhost:3000");
    expect(typedHref(" keeper@example.com ")).toBe("mailto:keeper@example.com");
    // A scheme that a link may not lead to stays one.
    expect(typedHref("tel:5551234")).toBeUndefined();
    expect(typedHref("javascript:alert(1)")).toBeUndefined();
  });

  test("whose host is beyond ASCII has it written as a browser leads to it, and is refused where a backslash ends the host", () => {
    expect(storedHref("https://café.example/menu")).toBe(
      "https://xn--caf-dma.example/menu",
    );
    expect(storedHref("https://café.example\\evil.com/path")).toBeUndefined();
    expect(storedHref("https://cafe.example\\evil.com/path")).toBeUndefined();
  });
});
