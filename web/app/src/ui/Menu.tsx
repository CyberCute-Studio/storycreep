// The app's menus of actions, as a board's in the tree of a project: Radix's
// dropdown menu, which the keyboard opens and moves through with the arrow
// keys, and a tap or a click opens, and whose items are at least 44 pixels
// high, the size of a fingertip (PRD FR-EDIT-09), and 8 pixels apart, so
// that a tap a little off one does not press its neighbour (NFR-TOUCH-01).
// It is not modal, so that a dialog that an item opens gets the focus and
// the pointer as the menu closes, rather than finding the page held inert
// by the menu.

import { DropdownMenu } from "radix-ui";
import type { ComponentProps, ReactNode } from "react";

/**
 * The classes of a menu's items, the one that the keyboard or the pointer is
 * on marked, but for the colour of their text, which MenuItem gives each
 * item alone: Tailwind writes the utilities of one property in the order of
 * their names, not of the classes, so of text-fg and text-danger on one
 * item, text-fg would win.
 */
const item =
  "flex min-h-11 cursor-pointer items-center gap-2 rounded px-3 outline-none data-[disabled]:cursor-not-allowed data-[disabled]:opacity-60 data-[highlighted]:bg-surface-hover";

/** The classes of a menu's box, whose items are one under another, 8 pixels apart (NFR-TOUCH-01). */
export const menuClass =
  "z-10 flex min-w-48 flex-col gap-2 rounded-md border border-border bg-canvas p-1 shadow-lg";

export interface MenuProps {
  /** The trigger, a Button, which the menu opens from. */
  trigger: ReactNode;
  /** Whether the trigger is disabled, which Radix gives it, and which opens nothing, as by a pointer's press, which a disabled button still hears. */
  disabled?: boolean;
  children: ReactNode;
}

/** A menu that trigger opens, of children, its items. */
export function Menu({ trigger, disabled, children }: MenuProps) {
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild disabled={disabled}>
        {trigger}
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={4}
          collisionPadding={8}
          className={menuClass}
        >
          {children}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

export interface MenuItemProps
  extends Omit<ComponentProps<typeof DropdownMenu.Item>, "className"> {
  /** A destructive action, as deleting. */
  danger?: boolean;
}

/** An item of a Menu, which onSelect acts on. */
export function MenuItem({ danger = false, ...props }: MenuItemProps) {
  return (
    <DropdownMenu.Item
      className={`${item} ${danger ? "text-danger" : "text-fg"}`}
      {...props}
    />
  );
}

/** A line between the groups of a Menu's items. */
export function MenuSeparator() {
  return <DropdownMenu.Separator className="h-px bg-border" />;
}
