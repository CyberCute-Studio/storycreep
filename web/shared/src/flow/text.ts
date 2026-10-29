// A rich text's plain text, its values resolved, as a runtime shows a node's
// text or a choice's (PRD §5.8.5, FR-TEXT-02), laid out as
// schema/rich-text/README.md says: the TypeScript twin of the Go package
// server/internal/richtext's Render.

import type { Segment, SegmentAttribute, SegmentVariable } from "../published";

/**
 * Returns the text of a document's segments as its plain text lays it out,
 * each value as value writes it: its texts and the names of its mentions, a
 * line feed for each paragraph and break segment, and for each item segment
 * but one that is the first segment. A list item starts with "• ", or its
 * number and ". ", after two spaces for each list that holds its list, and a
 * paragraph or a line break within an item is indented by two spaces for
 * each depth.
 */
export function render(
  segments: readonly Segment[],
  value: (s: SegmentVariable | SegmentAttribute) => string,
): string {
  let out = "";
  let depth = 0;
  segments.forEach((s, i) => {
    switch (s.kind) {
      case "text":
      case "mention":
        out += s.value;
        break;
      case "value":
        out += value(s);
        break;
      case "break":
        out += `\n${indent(depth)}`;
        break;
      case "paragraph":
        depth = s.depth ?? 0;
        out += `\n${indent(depth)}`;
        break;
      case "item":
        depth = s.depth;
        if (i > 0) {
          out += "\n";
        }
        out += indent(depth - 1);
        out += s.list === "ordered" ? `${s.number}. ` : "• ";
        break;
    }
  });
  return out;
}

// indent returns two spaces for each of n levels, and none for a level below
// 1.
function indent(n: number): string {
  return "  ".repeat(Math.max(n, 0));
}
