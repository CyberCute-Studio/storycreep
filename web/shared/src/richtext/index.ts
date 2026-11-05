// The rich text of a project's content (PRD FR-TEXT-01, ADR-020). See
// parse.ts.

export {
  checkHref,
  emptyRichText,
  isControlCode,
  MAX_BYTES,
  MAX_CHARS,
  MAX_DECIMALS,
  MAX_HREF_CHARS,
  MAX_LIST_DEPTH,
  MAX_START,
  parseRichText,
  RichTextError,
  type RichTextReason,
  richTextLength,
  stringifyRichText,
} from "./parse";
