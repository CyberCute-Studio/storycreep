// The editor of an element's text on the canvas (PRD FR-TEXT-01, FR-EDIT-09,
// NFR-TOUCH-04, plan note N68): the rich-text editor of the app's forms, in
// a box of the page's own over the node's card, which the canvas places in
// each frame that it draws, so that it follows the node as the canvas pans
// and zooms, as wide as the card on the screen, but no narrower than a line
// of text needs, nor wider than the canvas. It stays within the part of the
// canvas that the window shows, above the on-screen keyboard, as the visual
// viewport tells (AC3): where the node is beyond it, the box is at its edge,
// as high as that part leaves room for, and the editor within it scrolls to
// keep the caret in view as the user types; and where the keyboard leaves
// too little of the canvas in view, the page scrolls the canvas up to the
// top of what the window shows. It saves the text, in one command, as the
// user asks, and the canvas then has the focus again; while it holds text
// that is not saved, it stays open in place of another node's editor that
// the user asks for, and says to save or cancel it first.

import type { Node as BoardNode, RichTextDoc } from "@storycreep/shared/api";
import {
  type FormEvent,
  type Ref,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { ConflictNotice, useEntityForm } from "../content/form";
import { useDocumentState, useProjectDocument } from "../document/react";
import { payloadField } from "../project/dialogs";
import type { RichTextValue } from "../richtext/convert";
import { RichTextEditor } from "../richtext/RichTextEditor";
import { Button } from "../ui/Button";
import { Alert } from "../ui/Page";
import type { CanvasController } from "./controller";
import type { Rect } from "./viewport";

/** The narrowest that the editor's box is, in CSS pixels, so that a line of text and its toolbar fit at any zoom. */
const minWidth = 320;

/** The room that the box leaves between itself and the edges of what the window shows of the canvas, in CSS pixels. */
const gap = 8;

/** The least of the canvas that the window shows before the page scrolls it into view, in CSS pixels. */
const leastShown = 240;

/** The part of the window that the user sees, in its CSS pixels from the top of the layout viewport, as the on-screen keyboard leaves it. */
export interface Band {
  top: number;
  bottom: number;
}

/** Returns the part of the window that the user sees, above the on-screen keyboard, as the visual viewport tells, or the window's whole height where the browser has none. */
export function visibleBand(): Band {
  const vv = window.visualViewport;
  const top = vv?.offsetTop ?? 0;
  return { top, bottom: top + (vv?.height ?? window.innerHeight) };
}

/**
 * Returns where the editor's box, of height, goes on canvas, whose top is
 * at canvas.top of the window, which shows band of it: over the node at
 * node, as wide as the node is on the screen but within minWidth and the
 * canvas, and within the part of the canvas that the window shows, no
 * higher than that leaves room for (FR-EDIT-09 AC3). All are in CSS pixels
 * of the canvas but canvas.top and the band's, which are of the window.
 */
export function boxPlace(
  node: Rect,
  canvas: { width: number; height: number; top: number },
  band: Band,
  height: number,
): { x: number; y: number; width: number; maxHeight: number } {
  const width = Math.max(
    Math.min(minWidth, canvas.width - 2 * gap),
    Math.min(node.width, canvas.width - 2 * gap),
  );
  const shownTop = Math.max(0, band.top - canvas.top) + gap;
  const shownBottom = Math.min(canvas.height, band.bottom - canvas.top) - gap;
  const maxHeight = Math.max(0, shownBottom - shownTop);
  const x = Math.max(gap, Math.min(node.x, canvas.width - gap - width));
  const y = Math.max(
    shownTop,
    Math.min(node.y, shownBottom - Math.min(height, maxHeight)),
  );
  return { x, y, width, maxHeight };
}

/** What the page asks of the editor of an element's text that is open. */
export interface TextEditorHandle {
  /** Reports whether the editor holds text that is not saved, which closing it would lose. */
  changed(): boolean;
  /**
   * Keeps the editor open with its text, in place of the editor of another
   * node's that the user asked for, saying to save or cancel it first, and
   * gives it the focus.
   */
  keep(): void;
}

/** What the editor of an element's text is given. */
export interface TextEditorProps {
  projectId: string;
  node: BoardNode;
  controller: CanvasController;
  /** The canvas's element, whose part that the window shows the box stays in. */
  host: () => HTMLElement | null;
  /** Closes the editor, once it saved or was cancelled. */
  onClose: () => void;
  ref?: Ref<TextEditorHandle>;
}

const textField = payloadField("text");

/** Reports whether a and b are the same text, as the server compares them. */
function sameText(a: RichTextDoc | undefined, b: RichTextDoc | undefined) {
  return a === b || JSON.stringify(a) === JSON.stringify(b);
}

/** The editor of the text of node, over its card on the canvas. */
export function TextEditor({
  projectId,
  node,
  controller,
  host,
  onClose,
  ref,
}: TextEditorProps) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const box = useRef<HTMLFormElement>(null);
  const form = useEntityForm(projectId, node, (view, id) => view.nodes.get(id));
  const { commands, held, shown, against } = form;
  // What the editor holds, of the form shown as of key, which the form shown
  // anew starts again from.
  const text = useRef<{ key: string; value: RichTextValue }>(undefined);
  const [invalid, setInvalid] = useState<string>();
  // Whether the user asked for another node's text while this one held
  // text that is not saved, which the editor says (keep).
  const [kept, setKept] = useState(false);
  // What the editor holds now: what the user typed in the form shown, or
  // else what that form shows, which, once a save was refused for a change
  // made elsewhere, is what they typed over the text as it is now.
  const typed = () =>
    text.current?.key === held.key ? text.current.value : undefined;
  const current = () => typed()?.doc ?? shown.text;
  const changed = () =>
    typed()?.error !== undefined || !sameText(current(), against.text);
  const focusText = () =>
    box.current
      ?.querySelector<HTMLElement>('[contenteditable="true"]')
      ?.focus({ preventScroll: true });
  useImperativeHandle(ref, () => ({
    changed,
    keep: () => {
      setKept(true);
      focusText();
    },
  }));
  const refusal = refusalOf(commands.error, t, textField);
  const title = shown.title === "" ? t("canvas.node.untitled") : shown.title;
  // The names that the text's mentions and value references show, as the
  // cards show them.
  const components = useDocumentState(doc, (s) => s.view.components);
  const variables = useDocumentState(doc, (s) => s.view.variables);
  const names = useMemo(
    () => ({
      mention: (id: string) => components.get(id)?.name,
      value: (ref: string, attribute: string | undefined) =>
        attribute === undefined ? variables.get(ref)?.key : undefined,
    }),
    [components, variables],
  );

  // Where the box is: over the node, in each frame that the canvas draws,
  // within the part of the canvas that the window shows.
  useEffect(() => {
    let band = visibleBand();
    const place = (rectOf: (id: string) => Rect | undefined) => {
      const el = box.current;
      const h = host();
      const rect = rectOf(node.id);
      if (el === null || h === null || rect === undefined) {
        return;
      }
      const r = h.getBoundingClientRect();
      const p = boxPlace(
        rect,
        { width: r.width, height: r.height, top: r.top },
        band,
        el.offsetHeight,
      );
      el.style.transform = `translate(${p.x}px, ${p.y}px)`;
      el.style.width = `${p.width}px`;
      el.style.maxHeight = `${p.maxHeight}px`;
    };
    const stop = controller.follow(place);
    // What the window shows changes as the on-screen keyboard opens or
    // closes, or the window is resized, or the focus comes into the box:
    // where too little of the canvas is left in view for the box, the page
    // scrolls the canvas to the top of what it shows, and the box is placed
    // again; and again in the next frame, once the canvas has taken the
    // window's new height, which a resize gives it after the window tells of
    // it. A scroll, as the user's, or the caret's as they type, places the
    // box again alone, rather than scroll the page back to the canvas under
    // the user's finger.
    let again = 0;
    const shownChanged = () => {
      check();
      cancelAnimationFrame(again);
      again = requestAnimationFrame(check);
    };
    const scrolled = () => {
      band = visibleBand();
      controller.invalidate();
    };
    const check = () => {
      band = visibleBand();
      const h = host();
      const el = box.current;
      if (h !== null && el?.contains(document.activeElement)) {
        const r = h.getBoundingClientRect();
        const shown =
          Math.min(r.bottom, band.bottom) - Math.max(r.top, band.top);
        const room = Math.min(r.height, band.bottom - band.top);
        if (shown < Math.min(leastShown, room)) {
          window.scrollBy(0, r.top - band.top);
          band = visibleBand();
        }
      }
      controller.invalidate();
    };
    const vv = window.visualViewport;
    vv?.addEventListener("resize", shownChanged);
    vv?.addEventListener("scroll", scrolled);
    window.addEventListener("resize", shownChanged);
    window.addEventListener("scroll", scrolled);
    // The focus coming into the box, as the user taps its text, which
    // opens the keyboard: of the box as it is, which a save shows anew.
    const focusIn = (event: FocusEvent) => {
      if (box.current?.contains(event.target as Node)) {
        shownChanged();
      }
    };
    document.addEventListener("focusin", focusIn);
    return () => {
      stop();
      cancelAnimationFrame(again);
      vv?.removeEventListener("resize", shownChanged);
      vv?.removeEventListener("scroll", scrolled);
      window.removeEventListener("resize", shownChanged);
      window.removeEventListener("scroll", scrolled);
      document.removeEventListener("focusin", focusIn);
    };
  }, [controller, host, node.id]);

  // The box grows as the user types line after line, which no frame of the
  // canvas's tells of: the canvas places it again as its height changes,
  // so that it stays within the part of the canvas that the window shows,
  // above the on-screen keyboard, rather than grow under it.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the form shown anew, of another key, is another element.
  useEffect(() => {
    const el = box.current;
    if (el === null || typeof ResizeObserver !== "function") {
      return;
    }
    const grown = new ResizeObserver(() => controller.invalidate());
    grown.observe(el);
    return () => grown.disconnect();
  }, [controller, held.key]);

  // The editor has the focus as it opens, as a field that the user asked
  // to type in.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the focus is given as the editor opens alone.
  useEffect(() => {
    focusText();
  }, []);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (typed()?.error !== undefined) {
      form.refused();
      setInvalid(t("inspector.error.text"));
      return;
    }
    setInvalid(undefined);
    const value = current();
    if (value === undefined || sameText(value, against.text)) {
      onClose();
      return;
    }
    form.save(
      [
        {
          type: "node.update",
          payload: { id: shown.id, text: value },
          expected_rev: { [against.id]: against.rev },
        },
      ],
      { text: value },
    );
  };

  // Once the save has applied, the editor closes.
  const saved = commands.isSuccess;
  useEffect(() => {
    if (saved) {
      onClose();
    }
  }, [saved, onClose]);

  return (
    <form
      ref={(el) => {
        box.current = el;
        held.props.ref(el);
      }}
      key={held.key}
      onFocus={held.props.onFocus}
      onBlur={held.props.onBlur}
      onChange={held.props.onChange}
      onSubmit={submit}
      onKeyDown={(e) => {
        // Escape closes an editor whose text is as it was, as the inspector's
        // Cancel does; one whose text changed keeps it, to be saved, as one
        // whose save was refused for a change made elsewhere, which shows
        // what the user typed over the text as it is now.
        if (
          e.key === "Escape" &&
          !e.nativeEvent.isComposing &&
          !e.defaultPrevented &&
          !changed()
        ) {
          e.preventDefault();
          onClose();
        }
      }}
      noValidate
      aria-label={t("canvas.text_editor", { node: title })}
      className="pointer-events-auto absolute top-0 left-0 flex flex-col gap-2 overflow-y-auto overscroll-contain rounded-md border border-border bg-canvas p-2 text-fg shadow-lg"
    >
      <Alert
        message={form.overlaps.length > 0 ? undefined : refusal?.message}
      />
      <Alert message={kept ? t("canvas.text_kept") : undefined} />
      <ConflictNotice
        form={form}
        fieldName={() => t("inspector.text")}
        shownValue={() => undefined}
      />
      <RichTextEditor
        label={t("canvas.text_of", { node: title })}
        defaultValue={shown.text}
        error={invalid ?? refusal?.fields.text}
        names={names}
        onChange={(value) => {
          text.current = { key: held.key, value };
          held.props.onChange();
        }}
      />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("canvas.text_save")}
        </Button>
        <Button onClick={onClose}>{t("canvas.text_cancel")}</Button>
      </div>
    </form>
  );
}
