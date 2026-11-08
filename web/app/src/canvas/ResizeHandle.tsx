// The handle that resizes the one selected node on the canvas (PRD
// FR-EDIT-02, FR-EDIT-09, plan note N68): a square of 44 CSS pixels, the
// size of a fingertip, on the node's bottom right corner, which the canvas
// places in each frame that it draws, so that it follows the node as the
// canvas pans and zooms. A drag of it, of a finger, a pen's tip, or a
// mouse's primary button, resizes the node, on the board's grid where the
// canvas snaps, from 8 px of its press (NFR-TOUCH-05), and its release sends
// the size. The inspector's width and height do the same without a drag
// (WCAG 2.5.7), so the handle is hidden from assistive technology.

import { type PointerEvent, useEffect, useRef } from "react";
import { dragThreshold } from "../ui/drag";
import type { CanvasController } from "./controller";
import type { Rect } from "./viewport";

/** The handle's side, in CSS pixels (FR-EDIT-09). */
const side = 44;

/** The resize handle of the node of id. */
export function ResizeHandle({
  id,
  controller,
}: {
  id: string;
  controller: CanvasController;
}) {
  const handle = useRef<HTMLDivElement>(null);
  // The press that the handle follows: its pointer, where it went down, and
  // whether it has moved far enough to be a resize.
  const press = useRef<{
    pointer: number;
    x: number;
    y: number;
    resizing: boolean;
  }>(undefined);

  useEffect(
    () =>
      controller.follow((rectOf: (id: string) => Rect | undefined) => {
        const el = handle.current;
        if (el === null) {
          return;
        }
        const r = rectOf(id);
        // A node in the middle of a move has no handle until it lands.
        el.hidden = r === undefined || controller.moves();
        if (r !== undefined) {
          el.style.transform = `translate(${r.x + r.width - side / 2}px, ${
            r.y + r.height - side / 2
          }px)`;
        }
      }),
    [controller, id],
  );

  const end = (commit: boolean) => {
    const p = press.current;
    press.current = undefined;
    if (p?.resizing) {
      controller.resizeEnd(commit);
    }
  };

  const down = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) {
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    // A second pointer, as a second finger, or a mouse's click while a
    // finger resizes, ends the resize of the first, cancelled, as it ends a
    // move of the canvas's, and is followed by nothing, rather than take the
    // handle from the first, whose resize would else never end. The same
    // pointer, pressed again, whose release the handle did not hear of, is
    // followed anew.
    const was = press.current;
    if (was !== undefined) {
      end(false);
      if (was.pointer !== e.pointerId) {
        return;
      }
    }
    e.currentTarget.setPointerCapture?.(e.pointerId);
    press.current = {
      pointer: e.pointerId,
      x: e.clientX,
      y: e.clientY,
      resizing: false,
    };
  };
  const move = (e: PointerEvent<HTMLDivElement>) => {
    const p = press.current;
    if (p === undefined || p.pointer !== e.pointerId) {
      return;
    }
    // A mouse or a pen that moves without its button went up where the
    // handle did not hear of it, as once a menu of the system's took the
    // release: its resize ends, cancelled, as a move of the canvas's does
    // whose release it did not hear of, rather than size the node to where
    // the pointer is now.
    if (e.pointerType !== "touch" && (e.buttons & 1) === 0) {
      end(false);
      return;
    }
    const dx = e.clientX - p.x;
    const dy = e.clientY - p.y;
    if (!p.resizing) {
      if (Math.hypot(dx, dy) < dragThreshold) {
        return;
      }
      if (!controller.resizeStart(id)) {
        press.current = undefined;
        return;
      }
      p.resizing = true;
    }
    controller.resizeTo({ x: dx, y: dy });
  };
  const up = (e: PointerEvent<HTMLDivElement>) => {
    const p = press.current;
    if (p?.pointer === e.pointerId) {
      // The release's point, which a lift of a finger or a pen may give
      // after moves that the browser coalesced into it, is where the resize
      // ends, as a move of the canvas's ends under its pointer.
      if (p.resizing) {
        controller.resizeTo({ x: e.clientX - p.x, y: e.clientY - p.y });
      }
      end(true);
    }
  };
  const cancel = (e: PointerEvent<HTMLDivElement>) => {
    if (press.current?.pointer === e.pointerId) {
      end(false);
    }
  };

  // A handle that goes, as once the selection changes, ends its resize, as
  // cancelled, and so does the window's blur, as to another window, which
  // ends a move of the canvas's alike.
  useEffect(() => {
    const blur = () => {
      if (press.current?.resizing) {
        controller.resizeEnd(false);
      }
      press.current = undefined;
    };
    window.addEventListener("blur", blur);
    return () => {
      window.removeEventListener("blur", blur);
      blur();
    };
  }, [controller]);

  return (
    <div
      ref={handle}
      aria-hidden="true"
      hidden
      onPointerDown={down}
      onPointerMove={move}
      onPointerUp={up}
      onPointerCancel={cancel}
      onLostPointerCapture={cancel}
      className="pointer-events-auto absolute top-0 left-0 flex size-11 cursor-nwse-resize touch-none items-center justify-center"
    >
      <span className="block size-4 rounded-sm border-2 border-accent bg-canvas" />
    </div>
  );
}
