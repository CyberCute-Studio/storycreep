// The canvas's keys (PRD FR-EDIT-01, NFR-A11Y-04), while it has the focus:
// the arrows pan, by a tenth of the canvas, or half of it with Shift; + and
// - zoom in and out by a step, and 0 shows 100%, about the canvas's centre;
// Shift+1 fits the board's content and Shift+2 zooms to the selection;
// Escape ends a drag under way, cancelled, or else clears the selection
// (CanvasController's key); and, on a board that may be changed, Delete
// and Backspace delete the selected nodes and Enter opens the text of the
// one selected (plan note N68). None is held with Ctrl, Cmd, or Alt, whose
// combinations are the browser's, as Ctrl and + zoom the page (FR-EDIT-10
// AC2). Item 65 gives them command IDs and the help overlay.

/** What a key does on the canvas. */
export type KeyAction =
  | { kind: "pan"; dx: number; dy: number }
  | { kind: "zoom"; direction: 1 | -1 }
  | { kind: "actual" }
  | { kind: "fit" }
  | { kind: "selection" }
  | { kind: "deselect" }
  | { kind: "delete" }
  | { kind: "open" };

/** What keyAction reads of a key's event. */
export interface KeyInput {
  key: string;
  code: string;
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
}

/**
 * Returns what the key of e does on a canvas, in fractions of its width and
 * height for a pan, or undefined for a key that the canvas leaves to the
 * browser.
 */
export function keyAction(e: KeyInput): KeyAction | undefined {
  if (e.ctrlKey || e.metaKey || e.altKey) {
    return undefined;
  }
  // Shift and a digit go by the key's place, which the key's character, !
  // or @, or " on another layout, does not tell, and before the character:
  // Shift and 1 type + on a Swiss keyboard, where = and the keypad's + zoom
  // in still.
  if (e.shiftKey && e.code === "Digit1") {
    return { kind: "fit" };
  }
  if (e.shiftKey && e.code === "Digit2") {
    return { kind: "selection" };
  }
  const by = e.shiftKey ? 0.5 : 0.1;
  switch (e.key) {
    case "ArrowLeft":
      return { kind: "pan", dx: by, dy: 0 };
    case "ArrowRight":
      return { kind: "pan", dx: -by, dy: 0 };
    case "ArrowUp":
      return { kind: "pan", dx: 0, dy: by };
    case "ArrowDown":
      return { kind: "pan", dx: 0, dy: -by };
    case "+":
    case "=":
      return { kind: "zoom", direction: 1 };
    case "-":
    case "_":
      return { kind: "zoom", direction: -1 };
    case "Escape":
      return { kind: "deselect" };
    case "Delete":
    case "Backspace":
      return { kind: "delete" };
    case "Enter":
      return { kind: "open" };
  }
  if (!e.shiftKey && (e.key === "0" || e.code === "Digit0")) {
    return { kind: "actual" };
  }
  return undefined;
}
