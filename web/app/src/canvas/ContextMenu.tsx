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
// One that the keys opened has the focus on its first item, which Enter
// chooses, as a menu that a key opens from its button has, and one that a
// pointer opened has it on the menu itself: Radix tells which by the keys
// and the pointers that it hears of from when the menu's root is there
// (ContextMenus), which is before any menu is open.

import { DropdownMenu } from "radix-ui";
import type { ReactNode } from "react";
import { focusIsNowhere } from "../shell/focus";
import { menuClass } from "../ui/Menu";
import type { CanvasController } from "./controller";
import { besideBoardAnchor, boardAnchorClass, useBoardAnchor } from "./Links";
import type { Point } from "./viewport";

/**
 * What holds the canvas's context menu, children, a ContextMenu while one
 * is open, and nothing else: it is there from the first, before a menu is
 * asked for, so that it has heard the key, or the pointer, that asks for
 * one, by which the menu that the keys open has the focus on its first
 * item.
 */
export function ContextMenus({
  open,
  onClose,
  children,
}: {
  /** Whether a menu is open, as children then is one. */
  open: boolean;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <DropdownMenu.Root
      open={open}
      modal={false}
      onOpenChange={(next) => {
        if (!next) {
          onClose();
        }
      }}
    >
      {children}
    </DropdownMenu.Root>
  );
}

/** A menu of children, its items, at at of the board of controller, named label, in a ContextMenus that is open. */
export function ContextMenu({
  controller,
  at,
  label,
  returnFocus,
  children,
}: {
  controller: CanvasController;
  /** The point of the board that the menu was asked for at. */
  at: Point;
  /** What names the menu, as what it is of. */
  label: string;
  /** Gives the focus that the menu had back, to what had it as the menu opened, once the menu has gone. */
  returnFocus: () => void;
  children: ReactNode;
}) {
  const anchor = useBoardAnchor<HTMLSpanElement>(controller, at);
  return (
    <>
      {/* What the menu opens from is its point of the board, which nothing
      presses: the canvas opens it. */}
      <DropdownMenu.Trigger asChild>
        <span ref={anchor} aria-hidden="true" className={boardAnchorClass} />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          aria-label={label}
          // The menu is named by its label, rather than by its point.
          aria-labelledby={undefined}
          {...besideBoardAnchor}
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
          // The browser's own menu does not open over this one: the menu
          // key asks for it as it goes up, on Windows, where the focus is
          // by then, in this menu, which the key's going down opened.
          onContextMenu={(event) => event.preventDefault()}
          className={`${menuClass} text-fg`}
        >
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </>
  );
}
