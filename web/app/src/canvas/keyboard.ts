// The canvas's keys (PRD FR-EDIT-01, FR-EDIT-10, NFR-A11Y-04), while it has
// the focus, each a command of the app's (commands/catalog.ts, plan note
// N72), which the palette runs too: the arrows pan, by a tenth of the
// canvas, or half of it with Shift; + and - zoom in and out by a step, and
// 0 shows 100%, about the canvas's centre; Shift+1 fits the board's content
// and Shift+2 zooms to the selection; Escape ends a drag under way,
// cancelled, as it does with Shift, which is the browser's otherwise
// (escapeWithShift), or else clears the selection (CanvasController's
// command);
// and, on a board that may be changed, Delete and Backspace delete the
// selected nodes and Enter opens the text of the one selected (plan note
// N68). Letters do the toolbar's actions (N72), as E adds an element. None
// is held with Ctrl, Cmd, or Alt, whose combinations are the browser's, as
// Ctrl and + zoom the page (FR-EDIT-10 AC2), but for the clipboard's,
// which copy, cut, and paste nodes where they would copy, cut, and paste
// text (clipboardKey, plan note N71).

import { type CommandId, commandOfKey } from "../commands/catalog";
import type { KeyInput, Platform } from "../commands/keys";

export type { KeyInput };

/**
 * Returns the command of the canvas that the key of e is, on platform on,
 * the page's (usePlatform), or undefined for a key that the canvas leaves
 * to the page: one of those above, the menu key, or Shift+F10, which open
 * the context menu, on the canvas and in its accessibility tree
 * (NFR-A11Y-04, plan note N70), or a key of the clipboard (clipboardKey).
 */
export function canvasKey(e: KeyInput, on: Platform): CommandId | undefined {
  return commandOfKey(e, "canvas", on);
}

/**
 * Reports whether the key of e is Escape with Shift, without Ctrl, Cmd, or
 * Alt, which is no command of the canvas's, as Shift+Escape is Chrome's task
 * manager (the catalog's canvas.deselect), but which ends a drag under way,
 * cancelled, as Escape does, on the canvas and in its tree, rather than
 * leave it to land as Shift is held down through it: a rubber band that
 * Shift adds to the selection, or a move of the nodes that a press with
 * Shift added one to. The key stays the browser's.
 */
export function escapeWithShift(e: KeyInput): boolean {
  return (
    e.key === "Escape" && e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey
  );
}

/** What a key asks of the page's clipboard (clipboardKey). */
export type ClipboardAsk = "copy" | "cut" | "paste";

/**
 * Returns what the key of the command id, as canvasKey reads it, asks of
 * the clipboard, on the canvas and in its accessibility tree (FR-EDIT-07,
 * plan notes N71 and N72): Ctrl and C, or Cmd and C on macOS, copies the
 * selected nodes, X cuts them, and V pastes, without Shift or Alt, as they
 * copy, cut, and paste text, by the letter that the key types, or, on a
 * layout whose letters are not Latin, as Cyrillic's, by its place, as the
 * browser's own copy goes by. The canvas takes the key itself, rather than
 * the browser's copy event that would follow it, which a browser sends with
 * nothing of the page selected, as on the canvas, to the page's body, or
 * not at all, and which, cancelled without data, as the canvas would cancel
 * it, empties the system's clipboard; the page's clipboard holds the nodes.
 */
export function clipboardKey(
  id: CommandId | undefined,
): ClipboardAsk | undefined {
  switch (id) {
    case "canvas.copy":
      return "copy";
    case "canvas.cut":
      return "cut";
    case "canvas.paste":
      return "paste";
  }
  return undefined;
}
