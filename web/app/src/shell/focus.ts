// Where the keyboard's focus goes when the app shows another page, when the
// control that has it goes or is disabled, and when the skip link is
// followed.

/** The ID of the page's content, its main landmark, where the skip link leads. */
export const CONTENT_ID = "main";

/**
 * Reports whether no control of doc has the focus: the document's body has
 * it, as once the control that had it went, or the page's content itself,
 * which gets it then (focusContentIfLost).
 */
export function focusIsNowhere(doc: Document): boolean {
  const focused = doc.activeElement;
  return focused === null || focused === doc.body || focused.id === CONTENT_ID;
}

/**
 * Gives the page's content the focus if no control has it, as when the
 * control that had it, as a link, went with the page that it was on: the
 * focus is then on the document's body, from where a screen reader says
 * nothing of the new page, and the next Tab starts again from the top (WCAG
 * 2.4.3). A control that is still there, as the header's link, keeps it.
 */
export function focusContentIfLost(doc: Document): void {
  if (focusIsNowhere(doc)) {
    // The router restores the scroll position of the page.
    doc.getElementById(CONTENT_ID)?.focus({ preventScroll: true });
  }
}

/**
 * Keeps the keyboard's focus on the page that frame shows, its header and
 * its content, once the control of it that has the focus goes, as a revoked
 * session's item with its button, or is disabled, as a button while the
 * request that it made is answered, which Chromium takes the focus from: the
 * page's content gets the focus then (focusContentIfLost), rather than the
 * document's body, and a control that was disabled gets it back once it is
 * enabled again, unless the user has moved it since. A dialog's controls,
 * which are outside frame, are the dialog's: the control of the page that
 * opened it gets the focus back as it closes, or the content, if that
 * control has gone meanwhile, as a revoked token's Revoke, and a control
 * of the dialog that was disabled gets it back once it is enabled again,
 * as a confirmation's button once its request has been refused. It returns
 * what stops it.
 */
export function keepFocusOnPage(frame: HTMLElement): () => void {
  const doc = frame.ownerDocument;
  // The control of the page that had the focus last, which a dialog that it
  // opened has it of now.
  let focused: Element | null = null;
  // The control that was disabled while it had the focus, until it is
  // enabled again, or goes, or the user moves the focus: one of the page's,
  // or one of a dialog's, as its button while the request that it made is
  // answered, which Radix leaves the focus of on the body.
  let disabled: HTMLElement | null = null;
  const onFocusIn = (event: FocusEvent) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    if (target.id !== CONTENT_ID) {
      disabled = null;
    }
    if (frame.contains(target)) {
      focused = target;
    }
  };
  const onFocusOut = (event: FocusEvent) => {
    const target = event.target;
    if (
      event.relatedTarget === null &&
      target instanceof HTMLElement &&
      target.matches(":disabled")
    ) {
      disabled = target;
      // Once the browser has moved the focus to the body, the page's
      // content has it, unless the control is a dialog's, whose content is
      // all that the user can reach while it is open.
      if (frame.contains(target)) {
        queueMicrotask(() => focusContentIfLost(doc));
      }
    }
  };
  // A control goes with an element that the page removes, as one of a
  // dialog that its item took with it, and is enabled again as an attribute
  // changes.
  const observer = new MutationObserver(() => {
    if (focused !== null && !focused.isConnected && focusIsNowhere(doc)) {
      focused = null;
      focusContentIfLost(doc);
    }
    if (disabled === null) {
      return;
    }
    if (!disabled.isConnected) {
      disabled = null;
    } else if (!disabled.matches(":disabled") && focusIsNowhere(doc)) {
      const control = disabled;
      disabled = null;
      control.focus({ preventScroll: true });
    }
  });
  observer.observe(doc.body, {
    subtree: true,
    childList: true,
    attributes: true,
    attributeFilter: ["disabled"],
  });
  doc.addEventListener("focusin", onFocusIn);
  doc.addEventListener("focusout", onFocusOut);
  return () => {
    observer.disconnect();
    doc.removeEventListener("focusin", onFocusIn);
    doc.removeEventListener("focusout", onFocusOut);
  };
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
