// The handle that resizes the one selected node on the canvas (PRD
// FR-EDIT-02, FR-EDIT-09, plan note N68): a square of 44 CSS pixels, the
// size of a fingertip, on the node's bottom right corner, which the canvas
// places in each frame that it draws, so that it follows the node as the
// canvas pans and zooms. A press of it is the canvas's, which follows its
// pointer as it does every pointer, by the canvas's gestures, rather than
// by a drag of the handle's own: its drag, of a finger, a pen's tip, or a
// mouse's primary button, from 8 px of its press, or a mouse's 4
// (NFR-TOUCH-05), resizes the node, on the board's grid where the canvas
// snaps, with its corner under the pointer however the board moves under
// it, and its release sends the size; a second finger, which pinches, or
// another pointer's press, which takes the canvas, ends it, cancelled, as
// it ends a move. A node that the canvas draws less than twice the handle's
// side wide or high, as at a low zoom, has none, which would cover most of
// it, and the nodes beside it, so that a press there moves, selects, or
// pans as without a handle: the user zooms in, or sizes it in the
// inspector (plan note N68). The inspector's width and height do the same
// without a drag (WCAG 2.5.7), so the handle is hidden from assistive
// technology.

import { type PointerEvent, useEffect, useLayoutEffect, useRef } from "react";
import type { CanvasController, ScreenRectOf } from "./controller";

/** The handle's side, in CSS pixels (FR-EDIT-09). */
export const resizeHandleSide = 44;

/**
 * Reports whether a node that the canvas draws at r is large enough for a
 * handle on its corner, which covers a quarter of its width and of its
 * height at most then: at least twice the handle's side either way.
 */
export function hasRoomForHandle(r: {
  width: number;
  height: number;
}): boolean {
  return r.width >= 2 * resizeHandleSide && r.height >= 2 * resizeHandleSide;
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

  useEffect(
    () =>
      controller.follow((rectOf: ScreenRectOf) => {
        const el = handle.current;
        if (el === null) {
          return;
        }
        const r = rectOf(id);
        // A node in the middle of a move has no handle until it lands, nor
        // one drawn too small for it, but for the one being resized, whose
        // handle stays under its pointer.
        el.hidden =
          r === undefined ||
          controller.moves() ||
          (!hasRoomForHandle(r) && !controller.resizes(id));
        if (r !== undefined) {
          const half = resizeHandleSide / 2;
          el.style.transform = `translate(${r.x + r.width - half}px, ${
            r.y + r.height - half
          }px)`;
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
      className="pointer-events-auto absolute top-0 left-0 flex size-11 cursor-nwse-resize touch-none items-center justify-center"
    >
      <span className="block size-4 rounded-sm border-2 border-accent bg-canvas" />
    </div>
  );
}
