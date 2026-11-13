import type { RichTextDoc } from "@storycreep/shared/api";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { isEmptyText, RichText, type TextNames } from "./RichText";

// A rich text as the script view reads it: the page's own HTML of its
// blocks and marks, its references named as the cards name them.

const names: TextNames = {
  componentName: (id) => (id === "cmp_elara" ? "Elara" : undefined),
  valueName: (ref) => (ref === "var_gold" ? "gold" : undefined),
};

describe("the script view's rich text", () => {
  it("shows paragraphs, lists, line breaks, marks, links, mentions, and value references", () => {
    const doc: RichTextDoc = {
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            { type: "text", text: "Bold", marks: [{ type: "bold" }] },
            { type: "hardBreak" },
            {
              type: "text",
              text: "away",
              marks: [
                { type: "italic" },
                { type: "link", attrs: { href: "https://example.org" } },
              ],
            },
            { type: "mention", attrs: { id: "cmp_elara" } },
            { type: "valueRef", attrs: { ref: "var_gold", decimals: 2 } },
            { type: "mention", attrs: { id: "cmp_gone" } },
          ],
        },
        {
          type: "orderedList",
          attrs: { start: 3 },
          content: [
            {
              type: "listItem",
              content: [
                {
                  type: "paragraph",
                  content: [{ type: "text", text: "Third" }],
                },
              ],
            },
          ],
        },
        // A block of a type that a newer server may send shows nothing.
        { type: "table" } as unknown as RichTextDoc["content"][number],
      ],
    };
    const { container } = render(<RichText doc={doc} names={names} />);
    expect(container.querySelector("strong")?.textContent).toBe("Bold");
    expect(container.querySelector("br")).not.toBeNull();
    const link = container.querySelector("a");
    expect(link?.getAttribute("href")).toBe("https://example.org");
    expect(link?.getAttribute("rel")).toBe("noopener noreferrer");
    expect(link?.querySelector("em")?.textContent).toBe("away");
    expect(container.textContent).toContain("@Elara{gold}@…");
    const list = container.querySelector("ol");
    expect(list?.getAttribute("start")).toBe("3");
    expect(list?.textContent).toBe("Third");
  });

  it("tells a text of no words from one of some", () => {
    expect(isEmptyText({ type: "doc", content: [{ type: "paragraph" }] })).toBe(
      true,
    );
    expect(
      isEmptyText({
        type: "doc",
        content: [
          { type: "bulletList", content: [{ type: "listItem", content: [] }] },
        ],
      }),
    ).toBe(true);
    expect(
      isEmptyText({
        type: "doc",
        content: [
          { type: "paragraph", content: [{ type: "text", text: "Hello" }] },
        ],
      }),
    ).toBe(false);
  });
});
