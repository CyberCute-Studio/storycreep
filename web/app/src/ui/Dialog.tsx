// The frame of the app's dialogs, as the error console's: Radix's dialog,
// which keeps the focus within it while it is open and gives it back to the
// control that opened it when it closes, over the page, which it dims.

import { AlertDialog, Dialog } from "radix-ui";
import type { ReactNode } from "react";

const overlay = "fixed inset-0 bg-backdrop/50";
const content =
  "fixed inset-x-2 top-4 mx-auto max-h-[calc(100dvh-2rem)] max-w-lg space-y-4 overflow-y-auto rounded-lg border border-border bg-canvas p-4 text-fg shadow-xl sm:inset-x-8 sm:top-12";

export interface DialogFrameProps {
  title: string;
  description?: string;
  /**
   * Whether a click or a tap outside the dialog, as on the dimmed page,
   * closes it, as it does unless it says false: a dialog that shows what
   * closing it loses for good, as a token's secret, which is never shown
   * again, closes only by its own controls, or Escape, which a user presses
   * to close it.
   */
  closesOutside?: boolean;
  /**
   * Called as the dialog closes, before the focus goes back to the control
   * that opened it, which a dialog without a Dialog.Trigger, as one that
   * outlives the control, has to give it itself (event.preventDefault).
   */
  onCloseAutoFocus?: (event: Event) => void;
  children: ReactNode;
}

/** The content of a Dialog.Root, with its title and description. */
export function DialogFrame({
  title,
  description,
  closesOutside = true,
  onCloseAutoFocus,
  children,
}: DialogFrameProps) {
  return (
    <Dialog.Portal>
      <Dialog.Overlay className={overlay} />
      <Dialog.Content
        className={content}
        onCloseAutoFocus={onCloseAutoFocus}
        onInteractOutside={
          closesOutside ? undefined : (event) => event.preventDefault()
        }
        // Radix warns of a dialog without a description unless it is told
        // that it has none, with an aria-describedby of undefined.
        {...(description === undefined
          ? { "aria-describedby": undefined }
          : {})}
      >
        <Dialog.Title className="text-xl font-semibold">{title}</Dialog.Title>
        {description !== undefined && (
          <Dialog.Description className="text-fg-muted">
            {description}
          </Dialog.Description>
        )}
        {children}
      </Dialog.Content>
    </Dialog.Portal>
  );
}

/**
 * The content of an AlertDialog.Root, which asks before a destructive
 * action, as revoking a token: its title, what the action does, and the
 * buttons, of which the one that cancels has the focus first.
 */
export function ConfirmFrame({
  title,
  description,
  children,
}: DialogFrameProps) {
  return (
    <AlertDialog.Portal>
      <AlertDialog.Overlay className={overlay} />
      <AlertDialog.Content className={content}>
        <AlertDialog.Title className="text-xl font-semibold">
          {title}
        </AlertDialog.Title>
        {description !== undefined && (
          <AlertDialog.Description>{description}</AlertDialog.Description>
        )}
        {children}
      </AlertDialog.Content>
    </AlertDialog.Portal>
  );
}
