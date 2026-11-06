// A panel of a page (PRD NFR-TOUCH-06): what the page shows of one thing of
// a list beside it, as a variable's editor beside the project's variables.
// Where the window is at least 900 pixels wide, it is docked beside the
// list; below, it is a sheet over the page, from the bottom of the window,
// which holds the focus until it closes, as a dialog does, while the rest of
// the page is inert. It is the same element either way, so that turning a
// tablet, whose window crosses 900 pixels as it turns, keeps what the user
// typed in it and the focus, rather than showing it anew.

import {
  type KeyboardEvent,
  type ReactNode,
  useId,
  useLayoutEffect,
  useRef,
  useSyncExternalStore,
} from "react";
import { useTranslation } from "react-i18next";
import { focusContentIfLost } from "../shell/focus";
import { Button } from "./Button";

/** The width of the window, in CSS pixels, from which a panel is docked rather than a sheet. */
export const dockedFrom = 900;

const dockedQuery = `(min-width: ${dockedFrom}px)`;

function subscribe(changed: () => void): () => void {
  if (typeof window.matchMedia !== "function") {
    return () => undefined;
  }
  const query = window.matchMedia(dockedQuery);
  query.addEventListener("change", changed);
  return () => query.removeEventListener("change", changed);
}

/**
 * Reports whether the window is wide enough for a docked panel, as it
 * changes: a browser without matchMedia, as the tests' jsdom, has the room.
 */
export function useDocked(): boolean {
  return useSyncExternalStore(subscribe, () =>
    typeof window.matchMedia === "function"
      ? window.matchMedia(dockedQuery).matches
      : true,
  );
}

/**
 * Makes everything of the page but within inert, as a modal dialog makes it
 * (WCAG 2.4.3, 2.4.11): every sibling of within and of each element that
 * holds it, up to the document's body, which the focus and the pointer then
 * pass over, and a screen reader too. It returns what undoes it, which
 * leaves inert what was inert already.
 */
function inertAround(within: HTMLElement): () => void {
  const made: Element[] = [];
  for (
    let element: HTMLElement | null = within;
    element !== null && element !== element.ownerDocument.body;
    element = element.parentElement
  ) {
    for (const sibling of element.parentElement?.children ?? []) {
      if (sibling !== element && !sibling.hasAttribute("inert")) {
        sibling.setAttribute("inert", "");
        made.push(sibling);
      }
    }
  }
  return () => {
    for (const element of made) {
      element.removeAttribute("inert");
    }
  };
}

export interface PanelProps {
  /** The panel's heading, which names it as a region or a dialog. */
  title: string;
  /** What the heading says under it, if anything, as a variable's scope. */
  subtitle?: string;
  /**
   * Closes the panel: its Close button, Escape over a sheet, and a tap
   * beside a sheet ask for it. The page stops showing the panel then.
   */
  onClose: () => void;
  /**
   * Returns the control that gets the focus once the panel has gone, as the
   * list's item that opened it, or null for none, as once the item has gone,
   * when the page's content gets it.
   */
  returnFocus: () => HTMLElement | null;
  children: ReactNode;
}

/**
 * A panel, which shows its heading with the focus as it opens, so that a
 * keyboard or a screen reader is in it, rather than in the list that it is
 * beside, which may be a thousand items long; a page shows another thing's
 * panel anew, keyed by the thing, which gives that one's heading the focus
 * too. Once it has gone, the focus goes back to what returnFocus finds.
 */
export function Panel({
  title,
  subtitle,
  onClose,
  returnFocus,
  children,
}: PanelProps) {
  const { t } = useTranslation();
  const titleId = useId();
  const sheet = !useDocked();
  const frame = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const back = useRef(returnFocus);
  back.current = returnFocus;
  useLayoutEffect(() => {
    const element = frame.current;
    if (!sheet || element === null) {
      return;
    }
    const undo = inertAround(element);
    // The focus in what became inert, as the list's, once the window was
    // made narrower, goes to the sheet, as the browser takes it from there.
    if (!element.contains(element.ownerDocument.activeElement)) {
      heading.current?.focus();
    }
    return undo;
  }, [sheet]);
  useLayoutEffect(() => {
    heading.current?.focus();
    return () => {
      // React undoes a component's effects in the order that it declares
      // them, so the page's other controls are no longer inert by now.
      const control = back.current();
      if (control?.isConnected) {
        control.focus();
      } else {
        focusContentIfLost(document);
      }
    };
  }, []);
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    // A key of what the panel opened in a portal, as a dialog, which closes
    // itself on Escape, comes through React here too, but is not the
    // sheet's.
    if (
      sheet &&
      event.key === "Escape" &&
      !event.defaultPrevented &&
      event.currentTarget.contains(event.target as Node)
    ) {
      event.preventDefault();
      onClose();
    }
  };
  return (
    <div ref={frame}>
      {sheet && (
        // A tap beside the sheet closes it, as beside a dialog; a keyboard
        // closes it with Escape or its button.
        <div
          aria-hidden="true"
          className="fixed inset-0 touch-none bg-backdrop/50"
          onClick={onClose}
        />
      )}
      <section
        // A region beside the list while docked, and a modal dialog while a
        // sheet, which its heading names either way.
        {...(sheet ? { role: "dialog", "aria-modal": true } : {})}
        aria-labelledby={titleId}
        onKeyDown={onKeyDown}
        className={`space-y-4 border border-border bg-canvas p-4 text-fg ${
          sheet
            ? "fixed inset-x-0 bottom-0 max-h-[85dvh] overflow-y-auto overscroll-contain rounded-t-lg shadow-xl"
            : "sticky top-4 max-h-[calc(100dvh-2rem)] overflow-y-auto rounded-lg"
        }`}
      >
        <div className="flex items-start gap-2">
          <div className="min-w-0 flex-1">
            <h2
              id={titleId}
              ref={heading}
              tabIndex={-1}
              className="text-xl font-semibold break-words"
            >
              {title}
            </h2>
            {subtitle !== undefined && (
              <p className="break-words text-fg-muted">{subtitle}</p>
            )}
          </div>
          <Button onClick={onClose}>{t("panel.close")}</Button>
        </div>
        {children}
      </section>
    </div>
  );
}
