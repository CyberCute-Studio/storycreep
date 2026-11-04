// The app's menus of actions, as a board's in the tree of a project: Radix's
// dropdown menu, which the keyboard opens and moves through with the arrow
// keys, and a tap or a click opens, and whose items are at least 44 pixels
// high, the size of a fingertip (PRD FR-EDIT-09). It is not modal, so that
// a dialog that an item opens gets the focus and the pointer as the menu
// closes, rather than finding the page held inert by the menu.

import { DropdownMenu } from "radix-ui";
import type { ComponentProps, ReactNode } from "react";

/** The classes of a menu's items, the one that the keyboard or the pointer is on marked. */
const item =
  "flex min-h-11 cursor-pointer items-center gap-2 rounded px-3 text-fg outline-none data-[disabled]:cursor-not-allowed data-[disabled]:opacity-60 data-[highlighted]:bg-surface-hover";

export interface MenuProps {
  /** The trigger, a Button, which the menu opens from. */
  trigger: ReactNode;
  children: ReactNode;
}

/** A menu that trigger opens, of children, its items. */
export function Menu({ trigger, children }: MenuProps) {
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={4}
          collisionPadding={8}
          className="z-10 min-w-48 rounded-md border border-border bg-canvas p-1 shadow-lg"
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
      className={`${item} ${danger ? "text-danger" : ""}`}
      {...props}
    />
  );
}

/** A line between the groups of a Menu's items. */
export function MenuSeparator() {
  return <DropdownMenu.Separator className="my-1 h-px bg-border" />;
}
