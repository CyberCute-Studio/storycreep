import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./copy";

afterEach(() => {
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

describe("copyText", () => {
  it("copies with the Clipboard API where the page has it", async () => {
    const writeText = vi.fn(async () => {});
    expect(
      await copyText("report", document.body, {
        document,
        clipboard: { writeText },
      }),
    ).toBe(true);
    expect(writeText).toHaveBeenCalledWith("report");
  });

  it("copies from a selected text area without it, and gives the focus back", async () => {
    const container = document.createElement("div");
    const button = document.createElement("button");
    container.append(button);
    document.body.append(container);
    button.focus();
    let selected = "";
    const execCommand = vi.fn((command: string) => {
      const area = container.querySelector("textarea");
      selected =
        area?.value.slice(area.selectionStart, area.selectionEnd) ?? "";
      return command === "copy";
    });
    Object.defineProperty(document, "execCommand", {
      value: execCommand,
      configurable: true,
    });
    const refused = {
      writeText: vi.fn(async () => Promise.reject(new Error("denied"))),
    };
    expect(
      await copyText("report", container, { document, clipboard: refused }),
    ).toBe(true);
    expect(selected).toBe("report");
    expect(container.querySelector("textarea")).toBeNull();
    expect(document.activeElement).toBe(button);

    execCommand.mockReturnValue(false);
    expect(await copyText("report", container, { document })).toBe(false);
  });
});
