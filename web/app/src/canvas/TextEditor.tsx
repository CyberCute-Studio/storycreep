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
// user asks, and the canvas then has the focus again.

import type { Node } from "@storycreep/shared/api";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
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

/** What the editor of an element's text is given. */
export interface TextEditorProps {
  projectId: string;
  node: Node;
  controller: CanvasController;
  /** The canvas's element, whose part that the window shows the box stays in. */
  host: () => HTMLElement | null;
  /** Closes the editor, once it saved or was cancelled. */
  onClose: () => void;
}

const textField = payloadField("text");

/** The editor of the text of node, over its card on the canvas. */
export function TextEditor({
  projectId,
  node,
  controller,
  host,
  onClose,
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
    // closes, or the page scrolls: where too little of the canvas is left in
    // view for the box, the page scrolls the canvas to the top of what it
    // shows, and the box is placed again.
    const shownChanged = () => {
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
    vv?.addEventListener("scroll", shownChanged);
    window.addEventListener("resize", shownChanged);
    window.addEventListener("scroll", shownChanged);
    const el = box.current;
    el?.addEventListener("focusin", shownChanged);
    return () => {
      stop();
      vv?.removeEventListener("resize", shownChanged);
      vv?.removeEventListener("scroll", shownChanged);
      window.removeEventListener("resize", shownChanged);
      window.removeEventListener("scroll", shownChanged);
      el?.removeEventListener("focusin", shownChanged);
    };
  }, [controller, host, node.id]);

  // The editor has the focus as it opens, as a field that the user asked
  // to type in.
  useEffect(() => {
    box.current
      ?.querySelector<HTMLElement>('[contenteditable="true"]')
      ?.focus({ preventScroll: true });
  }, []);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const typed =
      text.current?.key === held.key ? text.current.value : undefined;
    if (typed?.error !== undefined) {
      form.refused();
      setInvalid(t("inspector.error.text"));
      return;
    }
    setInvalid(undefined);
    const value = typed?.doc;
    if (
      value === undefined ||
      JSON.stringify(value) === JSON.stringify(against.text)
    ) {
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
        // Cancel does; one whose text changed keeps it, to be saved.
        if (
          e.key === "Escape" &&
          !e.nativeEvent.isComposing &&
          !e.defaultPrevented &&
          text.current?.key !== held.key
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
