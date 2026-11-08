// The handle that resizes the one selected node on the canvas (PRD
// FR-EDIT-02, FR-EDIT-09, plan note N68): a square of 44 CSS pixels, the
// size of a fingertip, on the node's bottom right corner, which the canvas
// places in each frame that it draws, so that it follows the node as the
// canvas pans and zooms, and which covers a quarter of the node's width and
// of its height at most, beyond the corner of a node that the zoom draws
// small, so that a press on the rest of the node moves it, or taps it, as
// it would without a handle, rather than resize it (handlePlace). A press
// of it is the canvas's, which follows its pointer as it does every
// pointer, by the canvas's gestures, rather than by a drag of the handle's
// own: its drag, of a finger, a pen's tip, or a mouse's primary button,
// from 8 px of its press, or a mouse's 4 (NFR-TOUCH-05), resizes the node,
// on the board's grid where the canvas snaps, with its corner under the
// pointer however the board moves under it, and its release sends the
// size; a second finger, which pinches, or another pointer's press, which
// takes the canvas, ends it, cancelled, as it ends a move. The inspector's
// width and height do the same without a drag (WCAG 2.5.7), so the handle
// is hidden from assistive technology.

import { type PointerEvent, useEffect, useLayoutEffect, useRef } from "react";
import type { CanvasController, ScreenRectOf } from "./controller";
import type { Point, Rect } from "./viewport";

/** The handle's side, in CSS pixels (FR-EDIT-09). */
const side = 44;

/** The side of the handle's mark, which shows where it is, in CSS pixels. */
const markSide = 16;

/**
 * Returns where the handle of a node that the canvas draws at r goes, in
 * its CSS pixels: its top left corner, centred on the node's bottom right
 * corner, but, of a node drawn less than twice the handle's side wide or
 * high, as at a low zoom, moved out from it, so that it covers a quarter of
 * the node's width and of its height at most, which leaves the rest of the
 * node to a drag that moves it, and to a tap; and its mark's, within it,
 * centred on the node's corner either way.
 */
export function handlePlace(r: Rect): Point & { mark: Point } {
  const across = Math.min(side / 2, r.width / 4);
  const down = Math.min(side / 2, r.height / 4);
  return {
    x: r.x + r.width - across,
    y: r.y + r.height - down,
    mark: { x: across - markSide / 2, y: down - markSide / 2 },
  };
}

/** The resize handle of the node of id. */
export function ResizeHandle({
  id,
  controller,
  onPress,
}: {
  id: string;
  controller: CanvasController;
  /** Takes a press of the handle of the node of id, which the canvas follows as its own (CanvasController's down). */
  onPress: (e: PointerEvent<HTMLDivElement>, id: string) => void;
}) {
  const handle = useRef<HTMLDivElement>(null);
  const mark = useRef<HTMLSpanElement>(null);

  useEffect(
    () =>
      controller.follow((rectOf: ScreenRectOf) => {
        const el = handle.current;
        if (el === null) {
          return;
        }
        const r = rectOf(id);
        // A node in the middle of a move has no handle until it lands.
        el.hidden = r === undefined || controller.moves();
        if (r !== undefined) {
          const at = handlePlace(r);
          el.style.transform = `translate(${at.x}px, ${at.y}px)`;
          mark.current?.style.setProperty(
            "transform",
            `translate(${at.mark.x}px, ${at.mark.y}px)`,
          );
        }
      }),
    [controller, id],
  );

  // A handle that goes, as once the selection changes, or the editor of
  // the node's text opens over it, ends its press, or its resize, as
  // cancelled (Gestures' handleGone), as the page takes it away, rather
  // than once the browser has painted the page without it, before which a
  // move of its pointer would start a resize of a node without a handle.
  useLayoutEffect(
    () => () => controller.gestures.handleGone(id),
    [controller, id],
  );

  return (
    <div
      ref={handle}
      aria-hidden="true"
      hidden
      // The wheel, the pinch, and the browser's menu over the handle are the
      // canvas's, as over the canvas (BoardCanvas's layer over it).
      data-canvas=""
      onPointerDown={(e) => onPress(e, id)}
      className="pointer-events-auto absolute top-0 left-0 size-11 cursor-nwse-resize touch-none"
    >
      {/* The mark, on the node's corner, takes no press of its own: one on
          the part of it over the node, beyond the handle's square, is the
          node's. */}
      <span
        ref={mark}
        className="pointer-events-none absolute top-0 left-0 block size-4 rounded-sm border-2 border-accent bg-canvas"
      />
    </div>
  );
}
