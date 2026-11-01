// Copying text to the clipboard. The Clipboard API exists only in a secure
// context, which a self-hosted instance served over plain HTTP on a LAN
// address is not (PRD NFR-SEC-15), so without it the text is copied from a
// selected text area, as browsers have always allowed.

export interface CopyEnvironment {
  /** The page's document. */
  document: Document;
  /** The Clipboard API, if the page has it. */
  clipboard?: Pick<Clipboard, "writeText">;
}

/**
 * Copies text to the clipboard, and resolves to whether it could. A text area
 * that it selects instead of the Clipboard API is added to container, where
 * the keyboard's focus may go, as a dialog that keeps the focus within it;
 * the element that had the focus has it again afterwards.
 */
export async function copyText(
  text: string,
  container: HTMLElement,
  env: CopyEnvironment = {
    document,
    clipboard: globalThis.isSecureContext ? navigator.clipboard : undefined,
  },
): Promise<boolean> {
  if (env.clipboard !== undefined) {
    try {
      await env.clipboard.writeText(text);
      return true;
    } catch {
      // A browser may refuse it, as without the page's focus; the text
      // area may still work.
    }
  }
  const doc = env.document;
  const focused = doc.activeElement;
  const area = doc.createElement("textarea");
  area.value = text;
  area.readOnly = true;
  // Out of sight, but not hidden, which would keep it from being selected.
  area.style.position = "fixed";
  area.style.insetBlockStart = "0";
  area.style.opacity = "0";
  container.append(area);
  try {
    area.select();
    // The only way to copy without the Clipboard API.
    return doc.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
    if (focused instanceof HTMLElement) {
      focused.focus();
    }
  }
}
