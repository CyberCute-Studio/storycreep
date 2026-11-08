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
// user asks, and the canvas then has the focus again, unless the user took
// it elsewhere meanwhile; while the save is under way, the text is not
// changed, nor the editor cancelled, so that what it closes with once the
// save applies is what was saved. While it holds text that is not saved,
// it stays open in place of another node's editor that the user asks for,
// and says to save or cancel it first, and while it saves its text, it
// gives way to that editor once the save applies. Once its node is no
// longer on the board, as once it was deleted, or the board can no longer
// be changed, it closes, unless it holds text that is not saved, which it
// keeps, saying why it cannot be saved, until the user cancels it, or the
// node is back, as once its deletion is undone.

import type { Node as BoardNode } from "@storycreep/shared/api";
import {
  type FormEvent,
  type Ref,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { ConflictNotice, useEntityForm } from "../content/form";
import { useDocumentState, useProjectDocument } from "../document/react";
import { equal } from "../document/reconcile";
import { payloadField } from "../project/dialogs";
import type { RichTextValue } from "../richtext/convert";
import { RichTextEditor } from "../richtext/RichTextEditor";
import { Button } from "../ui/Button";
import { Alert } from "../ui/Page";
import type { CanvasController, ScreenRectOf } from "./controller";
import { titleOf } from "./labels";
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
  /**
   * Asks the editor to give way to the editor of the text of the node of
   * id, which the user asked for, and reports whether it may close now, as
   * one whose text is as it was may. One whose save is under way gives way
   * once the save applies (onClose with id), or, once it is refused, keeps
   * its text, as one that holds text that is not saved does at once: open,
   * saying to save or cancel it first, with the focus, rather than lose it.
   */
  giveWay(id: string): boolean;
}

/** What the editor of an element's text is given. */
export interface TextEditorProps {
  projectId: string;
  node: BoardNode;
  controller: CanvasController;
  /** The canvas's element, whose part that the window shows the box stays in. */
  host: () => HTMLElement | null;
  /** Whether the node is no longer on the board, as once it was deleted, or moved to another board, which its text can no longer be saved to. */
  gone: boolean;
  /** Whether the board can no longer be changed, as once the user's role or the project's archive changed. */
  readOnly: boolean;
  /** Closes the editor, once it saved or was cancelled, or gives way to the editor of the node of next, once its save applied (giveWay). */
  onClose: (next?: string) => void;
  ref?: Ref<TextEditorHandle>;
}

const textField = payloadField("text");

/** The editor of the text of node, over its card on the canvas. */
export function TextEditor({
  projectId,
  node,
  controller,
  host,
  gone,
  readOnly,
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
  // How often the user asked for another node's text while this one held
  // text that is not saved, which the editor says, and gives its text the
  // focus for, once it has rendered, as once a sheet of the inspector that
  // asked for it, which holds the editor inert, has closed (giveWay); and
  // the node whose text the user asked for while the save was under way,
  // which the editor gives way to once the save applies.
  const [kept, setKept] = useState(0);
  const [after, setAfter] = useState<string>();
  // What the editor holds now: what the user typed in the form shown, or
  // else what that form shows, which, once a save was refused for a change
  // made elsewhere, is what they typed over the text as it is now; and
  // whether it changed, as the server compares texts, whatever the order of
  // their objects' fields (equal).
  const typed = () =>
    text.current?.key === held.key ? text.current.value : undefined;
  const current = () => typed()?.doc ?? shown.text;
  const changed = () =>
    typed()?.error !== undefined || !equal(current(), against.text);
  const focusText = useCallback(
    () =>
      box.current
        ?.querySelector<HTMLElement>('[contenteditable="true"]')
        ?.focus({ preventScroll: true }),
    [],
  );
  const keep = () => setKept((n) => n + 1);
  useEffect(() => {
    if (kept > 0) {
      focusText();
    }
  }, [kept, focusText]);
  // Why the text can no longer be saved, if it cannot.
  const unsavable = readOnly
    ? t("canvas.text_read_only")
    : gone
      ? t("canvas.text_gone")
      : undefined;
  const pending = commands.isPending;
  // An editor that can no longer save, with nothing to save, closes, as at
  // once of a node that another session deleted; one that holds text that
  // is not saved stays, saying why it cannot be saved, so that the text is
  // not lost unseen, as once the user deleted the node by mistake, which an
  // undo brings back, after which it saves again.
  // biome-ignore lint/correctness/useExhaustiveDependencies: changed reads what the editor holds as the node goes, or the save that was under way settles.
  useEffect(() => {
    if (unsavable !== undefined && !pending && !changed()) {
      onClose();
    }
  }, [unsavable, pending, onClose]);
  useImperativeHandle(ref, () => ({
    giveWay: (id) => {
      if (commands.isPending) {
        setAfter(id);
        return false;
      }
      if (!changed()) {
        return true;
      }
      keep();
      return false;
    },
  }));
  const refusal = refusalOf(commands.error, t, textField);
  const title = titleOf(shown, t);
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
  // within the part of the canvas that the window shows, and as it opens,
  // before the browser paints it, rather than once where it was not placed
  // yet, at the canvas's corner.
  useLayoutEffect(() => {
    let band = visibleBand();
    // What tells where the node is on the canvas, as the canvas's frames
    // give it, which a scroll places the box by again, alone, rather than
    // ask the canvas for a frame that draws the board as it was.
    let nodeRect: ScreenRectOf | undefined;
    const place = (rectOf: ScreenRectOf) => {
      nodeRect = rectOf;
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
      if (nodeRect === undefined) {
        controller.invalidate();
      } else {
        place(nodeRect);
      }
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
    if (pending || unsavable !== undefined) {
      return;
    }
    if (typed()?.error !== undefined) {
      form.refused();
      setInvalid(t("inspector.error.text"));
      return;
    }
    setInvalid(undefined);
    const value = current();
    if (value === undefined || equal(value, against.text)) {
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

  // Once the save has applied, the editor closes, or gives way to the
  // editor that the user asked for meanwhile; once it is refused, it keeps
  // what the user typed, and says so of the editor asked for.
  const saved = commands.isSuccess;
  const refused = commands.isError;
  useEffect(() => {
    if (saved) {
      onClose(after);
    }
  }, [saved, onClose, after]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: keep is of the render that the refusal came in.
  useEffect(() => {
    if (refused && after !== undefined) {
      setAfter(undefined);
      keep();
    }
  }, [refused, after]);

  // The form's element, for the box and for what the form holds, by a ref
  // of the same function from one render to the next: React calls a new
  // one with the element on each, as the form's ref of the form shown
  // anew, which gives the focus back to the control that had it, and so
  // would take it from what that control opened in a portal, as the rich
  // text's link dialog, whenever the editor rendered again.
  const heldRef = held.props.ref;
  const formRef = useCallback(
    (el: HTMLFormElement | null) => {
      box.current = el;
      heldRef(el);
    },
    [heldRef],
  );
  // The keys that went down in the editor, by their places, whose repeats,
  // as the user holds one, are the user's typing. A key held as the editor
  // opened, as the Enter on the canvas that opened it, goes on repeating
  // into what has the focus, the text once the editor has given it the
  // focus, where its repeats do nothing until it goes up, rather than add a
  // paragraph for each, which would leave text to save or cancel that the
  // user never typed.
  const pressed = useRef(new Set<string>());

  return (
    <form
      ref={formRef}
      key={held.key}
      onFocus={held.props.onFocus}
      onBlur={held.props.onBlur}
      onChange={held.props.onChange}
      onSubmit={submit}
      onKeyDownCapture={(e) => {
        if (!e.repeat) {
          pressed.current.add(e.code);
        } else if (!pressed.current.has(e.code)) {
          e.preventDefault();
          e.stopPropagation();
        }
      }}
      onKeyUpCapture={(e) => {
        pressed.current.delete(e.code);
      }}
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
      <Alert message={unsavable} />
      <Alert
        message={
          kept > 0 && unsavable === undefined
            ? t("canvas.text_kept")
            : undefined
        }
      />
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
        // What the user types while the save is under way would be lost as
        // the editor closes once it applies.
        editable={!pending}
        onChange={(value) => {
          text.current = { key: held.key, value };
          held.props.onChange();
        }}
      />
      <div className="flex flex-wrap gap-2">
        <Button
          type="submit"
          variant="primary"
          disabled={pending || unsavable !== undefined}
        >
          {t("canvas.text_save")}
        </Button>
        {/* A save under way is not cancelled, as a dialog's is not: the
        editor would close, and what became of the save be said nowhere. */}
        <Button disabled={pending} onClick={() => onClose()}>
          {t("canvas.text_cancel")}
        </Button>
      </div>
    </form>
  );
}
