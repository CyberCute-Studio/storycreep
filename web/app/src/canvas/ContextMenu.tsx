// The canvas's context menu (PRD FR-EDIT-09, NFR-TOUCH-02, plan note N70):
// what may be done to the nodes selected, to the connection selected, or
// on the board at a point of it, in a menu at that point of the board,
// which follows it as the canvas moves. A finger's press, or a pen's, held
// for 400 ms opens it (gestures.ts), and so do a mouse's right click and,
// with the keys, Shift+F10 and the menu key, on the canvas and in its
// accessibility tree, so that nothing in it is a right click's alone; each
// of its items is in the toolbar or the inspector too. It is the app's menu
// (ui/Menu.tsx), whose items are of a fingertip's size, and is not modal: a
// press outside it closes it, and is the canvas's, as on the board beside it.

import { DropdownMenu } from "radix-ui";
import { type ReactNode, useEffect, useRef } from "react";
import { focusIsNowhere } from "../shell/focus";
import { menuClass } from "../ui/Menu";
import type { CanvasController } from "./controller";
import type { Point } from "./viewport";

/** A menu of children, its items, at at of the board of controller, named label. */
export function ContextMenu({
  controller,
  at,
  label,
  onClose,
  returnFocus,
  children,
}: {
  controller: CanvasController;
  /** The point of the board that the menu was asked for at. */
  at: Point;
  /** What names the menu, as what it is of. */
  label: string;
  onClose: () => void;
  /** Gives the focus that the menu had back, to what had it as the menu opened, once the menu has gone. */
  returnFocus: () => void;
  children: ReactNode;
}) {
  const anchor = useRef<HTMLSpanElement>(null);
  useEffect(
    () =>
      controller.follow(() => {
        const el = anchor.current;
        if (el !== null) {
          const p = controller.screenPoint(at);
          el.style.transform = `translate(${p.x}px, ${p.y}px)`;
        }
      }),
    [controller, at],
  );
  return (
    <DropdownMenu.Root
      open
      modal={false}
      onOpenChange={(open) => {
        if (!open) {
          onClose();
        }
      }}
    >
      {/* What the menu opens from is its point of the board, which nothing
      presses: the canvas opens it. */}
      <DropdownMenu.Trigger asChild>
        <span
          ref={anchor}
          aria-hidden="true"
          className="absolute top-0 left-0 size-px"
        />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          aria-label={label}
          // The menu is named by its label, rather than by its point.
          aria-labelledby={undefined}
          side="right"
          align="start"
          sideOffset={8}
          collisionPadding={8}
          // The menu follows its point of the board in each frame, as the
          // wheel pans the canvas under it.
          updatePositionStrategy="always"
          // What had the focus as the menu opened takes it back, the canvas
          // or the item of its tree, where no control has it, as on the
          // page's body, or on its content, which the shell gives it once
          // what had it before the menu went, as the tree's item of a node
          // that the menu deleted: not the focus that an item of the menu,
          // or a press outside it, gave another thing meanwhile, as the
          // editor of a text, or a field of the inspector, whose keys, as
          // Backspace, would else go to the canvas, and delete the
          // selection.
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            if (focusIsNowhere(document)) {
              returnFocus();
            }
          }}
          className={`${menuClass} text-fg`}
        >
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
