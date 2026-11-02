// Where the keyboard's focus goes when the app shows another page, and when
// the skip link is followed.

/** The ID of the page's content, its main landmark, where the skip link leads. */
export const CONTENT_ID = "main";

/**
 * Gives the page's content the focus if nothing has it, as when the control
 * that had it, as a link, went with the page that it was on: the focus is
 * then on the document's body, from where a screen reader says nothing of
 * the new page, and the next Tab starts again from the top (WCAG 2.4.3). A
 * control that is still there, as the header's link, keeps it.
 */
export function focusContentIfLost(doc: Document): void {
  const focused = doc.activeElement;
  if (focused === null || focused === doc.body) {
    // The router restores the scroll position of the page.
    doc.getElementById(CONTENT_ID)?.focus({ preventScroll: true });
  }
}

/**
 * Follows the skip link: gives the page's content the focus, as the link's
 * #main would, without changing the URL. The fragment of a link's page holds
 * the link's token, as an invitation's (auth/fragment.ts), which following
 * #main would replace, and the page would lose.
 */
export function skipToContent(event: {
  preventDefault(): void;
  currentTarget: { ownerDocument: Document };
}): void {
  event.preventDefault();
  event.currentTarget.ownerDocument.getElementById(CONTENT_ID)?.focus();
}
