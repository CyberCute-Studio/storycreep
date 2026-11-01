import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./copy";

// The document's own execCommand, if it has one, which a test replaces and
// afterEach puts back, as vi.restoreAllMocks does not.
const execCommand = Object.getOwnPropertyDescriptor(document, "execCommand");

afterEach(() => {
  vi.restoreAllMocks();
  if (execCommand === undefined) {
    Reflect.deleteProperty(document, "execCommand");
  } else {
    Object.defineProperty(document, "execCommand", execCommand);
  }
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

    // As Safari on iOS, where select() selects nothing of a read-only field.
    vi.spyOn(HTMLTextAreaElement.prototype, "select").mockImplementation(
      () => {},
    );
    selected = "";
    expect(await copyText("report", container, { document })).toBe(true);
    expect(selected).toBe("report");
    expect(document.activeElement).toBe(button);

    execCommand.mockReturnValue(false);
    expect(await copyText("report", container, { document })).toBe(false);
  });
});
