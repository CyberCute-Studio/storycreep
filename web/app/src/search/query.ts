// The query of a project's search (PRD FR-PROJ-03, plan note N72), as the
// server takes it (operation searchProject): of 1 to 200 characters,
// without control characters. The page's address, its field, and the
// palette's search give it as the user wrote it, which may be longer, as
// what a paste made of the palette's field, which has no limit, or an
// address that a link spells, or hold a tab, as one pasted from a table,
// which a field of one line keeps: each is the query that searchText
// makes of it, rather than one that the server refuses.

import {
  isControlCode,
  isHighSurrogate,
  isLowSurrogate,
} from "@storycreep/shared/richtext";

/** How many characters a query has at most, as the server counts them, by Unicode's code points (search.MaxQueryLength). */
export const maxQueryLength = 200;

/**
 * Reports whether c, one of Unicode's code points, is one that the server
 * refuses in a query (input.IsControl): a control character, a tab among
 * them, a bidirectional control, or a line or paragraph separator; or half
 * of a pair of surrogates, which a text of UTF-8, as the address's, cannot
 * hold.
 */
function refused(c: string): boolean {
  if (c.length !== 1) {
    return false;
  }
  const code = c.charCodeAt(0);
  return (
    code === 0x09 ||
    isControlCode(code) ||
    isHighSurrogate(code) ||
    isLowSurrogate(code)
  );
}

/**
 * Returns text as the search's query: each character that the server
 * refuses a space, trimmed, and cut after maxQueryLength characters, or ""
 * for one of nothing else.
 */
export function searchText(text: string): string {
  const spaced = Array.from(text, (c) => (refused(c) ? " " : c))
    .join("")
    .trim();
  return Array.from(spaced).slice(0, maxQueryLength).join("").trimEnd();
}
