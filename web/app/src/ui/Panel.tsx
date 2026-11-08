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
  type RefObject,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { focusContentIfLost, focusIsNowhere } from "../shell/focus";
import { Button } from "./Button";
import { useMediaQuery } from "./media";

/** The width of the window, in CSS pixels, from which a panel is docked rather than a sheet. */
export const dockedFrom = 900;

const dockedQuery = `(min-width: ${dockedFrom}px)`;

/**
 * Reports whether the window is wide enough for a docked panel, as it
 * changes: a browser without matchMedia, as the tests' jsdom, has the room.
 */
export function useDocked(): boolean {
  return useMediaQuery(dockedQuery, true);
}

/**
 * The classes of what holds a page's list and the panel beside it while the
 * panel is open: the list and the docked panel side by side from the width
 * of a docked panel, and one under the other below it, where the panel is a
 * sheet over the page.
 */
export const besidePanel =
  "grid items-start gap-6 min-[900px]:grid-cols-[minmax(0,1fr)_24rem]";

/**
 * Returns what a page's panel shows of the item of selectedId, the one
 * that the page's address names, of items: the item as items have it, or,
 * while the confirmation of its deletion is open, as it was last, as the
 * project's document takes it away at once once its deletion is sent, so
 * that the confirmation stays, and says what became of it. The
 * confirmation tells onDeleting whether it is open (useDeleting).
 */
export function usePanelItem<T extends { id: string }>(
  items: ReadonlyMap<string, T>,
  selectedId: string | undefined,
): { shown: T | undefined; onDeleting: (deleting: boolean) => void } {
  const [deleting, setDeleting] = useState(false);
  const last = useRef<T>(undefined);
  const selected = selectedId === undefined ? undefined : items.get(selectedId);
  if (selected !== undefined) {
    last.current = selected;
  }
  return {
    shown:
      selected ??
      (deleting && last.current?.id === selectedId ? last.current : undefined),
    onDeleting: setDeleting,
  };
}

/**
 * Tells onDeleting true as a confirmation of a deletion opens, and false
 * once it has gone, however it goes: closed, or with its panel, as once the
 * deletion applied and the page's address no longer names what it deleted,
 * or as the address names another item, after the browser's Back, so that
 * the page no longer shows the item as it was (usePanelItem).
 */
export function useDeleting(onDeleting: (deleting: boolean) => void): void {
  useLayoutEffect(() => {
    onDeleting(true);
    return () => onDeleting(false);
  }, [onDeleting]);
}

/** Returns the app's own element that holds within: the child of the document's body that holds it. */
function appOf(within: HTMLElement): HTMLElement {
  let element = within;
  while (
    element.parentElement !== null &&
    element.parentElement !== element.ownerDocument.body
  ) {
    element = element.parentElement;
  }
  return element;
}

/**
 * Makes everything of the page but within inert, as a modal dialog makes it
 * (WCAG 2.4.3, 2.4.11): every sibling of within and of each element that
 * holds it, up to the app's own element, the document's body's child that
 * holds it, which the focus and the pointer then pass over, and a screen
 * reader too. The body's other children are the layers that the page opens
 * over itself in portals, as a dialog or a menu, which may have been opened
 * from the panel before it became a sheet, as when a tablet turns with a
 * dialog of the panel open, and which hold the focus themselves while they
 * are open. It returns what undoes it, which leaves inert what was inert
 * already.
 */
function inertAround(within: HTMLElement): () => void {
  const made: Element[] = [];
  const body = within.ownerDocument.body;
  for (
    let element: HTMLElement | null = within;
    element !== null &&
    element.parentElement !== null &&
    element.parentElement !== body;
    element = element.parentElement
  ) {
    for (const sibling of element.parentElement.children) {
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
   * Returns the control that gets the focus once the panel has gone, if the
   * panel had it, as the list's item that opened it, or null for none, as
   * once the item has gone, when the page's content gets it.
   */
  returnFocus: () => HTMLElement | null;
  /**
   * The panel's heading, which the page may give the focus again while the
   * panel is open, as once the user asks again for what it shows, as a
   * panel that opens gives it the focus.
   */
  headingRef?: RefObject<HTMLHeadingElement | null>;
  children: ReactNode;
}

/**
 * A panel, which shows its heading with the focus as it opens, so that a
 * keyboard or a screen reader is in it, rather than in the list that it is
 * beside, which may be a thousand items long; a page shows another thing's
 * panel anew, keyed by the thing, which gives that one's heading the focus
 * too. Once it has gone, the focus that it had goes back to what
 * returnFocus finds.
 */
export function Panel({
  title,
  subtitle,
  onClose,
  returnFocus,
  headingRef,
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
    // The focus in a layer that the page opened over itself, as a dialog
    // that the panel opened, stays there: the dialog holds it, and would
    // take it back from the sheet, selecting what was typed in its field.
    const active = element.ownerDocument.activeElement;
    const inApp =
      active === null ||
      active === element.ownerDocument.body ||
      appOf(element).contains(active);
    if (inApp && !element.contains(active)) {
      heading.current?.focus();
    }
    return undo;
  }, [sheet]);
  useLayoutEffect(() => {
    const element = frame.current;
    heading.current?.focus();
    return () => {
      // The focus goes back to the list from the panel alone, or from
      // nowhere: the focus elsewhere stays where it is, as in a dialog of
      // the list's folders while another session deletes what the panel
      // shows, which would take it back from the list's item, selecting
      // what was typed in its field.
      if (
        !focusIsNowhere(document) &&
        !(element?.contains(document.activeElement) ?? false)
      ) {
        return;
      }
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
    // sheet's; nor is an Escape that ends what an input method was
    // composing in one of the sheet's fields, as of Japanese or Chinese.
    if (
      sheet &&
      event.key === "Escape" &&
      !event.nativeEvent.isComposing &&
      !event.defaultPrevented &&
      event.currentTarget.contains(event.target as Node)
    ) {
      event.preventDefault();
      onClose();
    }
  };
  return (
    // A docked panel stays at the top of the window as the list beside it
    // scrolls, so that it is in view wherever its item is in a long list;
    // it is the element beside the list that does, as an element under it
    // has no room of its own to stay in.
    <div ref={frame} className={sheet ? undefined : "sticky top-4"}>
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
            : "max-h-[calc(100dvh-2rem)] overflow-y-auto rounded-lg"
        }`}
      >
        <div className="flex items-start gap-2">
          <div className="min-w-0 flex-1">
            <h2
              id={titleId}
              ref={(element) => {
                heading.current = element;
                if (headingRef !== undefined) {
                  headingRef.current = element;
                }
              }}
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
