// The frame of the app's dialogs, as the error console's: Radix's dialog,
// which keeps the focus within it while it is open and gives it back to the
// control that opened it when it closes, over the page, which it dims.

import { AlertDialog, Dialog } from "radix-ui";
import type { ReactNode } from "react";
import { focusContentIfLost } from "../shell/focus";
import { Button } from "./Button";

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
  onCloseAutoFocus,
  children,
}: DialogFrameProps) {
  return (
    <AlertDialog.Portal>
      <AlertDialog.Overlay className={overlay} />
      <AlertDialog.Content
        className={content}
        onCloseAutoFocus={onCloseAutoFocus}
      >
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

/**
 * The buttons of a confirmation (ConfirmFrame): Cancel, which has the focus
 * first, and the one that confirms, which is not an AlertDialog.Action,
 * which would close the dialog before its request has answered, both
 * disabled while it is under way (pending).
 */
export function ConfirmButtons({
  cancel,
  confirm,
  pending,
  onConfirm,
}: {
  cancel: string;
  confirm: string;
  pending: boolean;
  onConfirm: () => void;
}) {
  return (
    <div className="flex flex-wrap justify-end gap-2">
      <AlertDialog.Cancel asChild>
        <Button disabled={pending}>{cancel}</Button>
      </AlertDialog.Cancel>
      <Button variant="danger" disabled={pending} onClick={onConfirm}>
        {confirm}
      </Button>
    </div>
  );
}

/**
 * The buttons of a dialog's form: Cancel, a Dialog.Close, unless cancel is
 * undefined, as for a form that is not always in a dialog, and the one that
 * submits the form, both disabled while its request is under way (pending).
 */
export function FormButtons({
  cancel,
  submit,
  pending,
}: {
  cancel?: string;
  submit: string;
  pending: boolean;
}) {
  return (
    <div className="flex flex-wrap justify-end gap-2">
      {cancel !== undefined && (
        <Dialog.Close asChild>
          <Button disabled={pending}>{cancel}</Button>
        </Dialog.Close>
      )}
      <Button type="submit" variant="primary" disabled={pending}>
        {submit}
      </Button>
    </div>
  );
}

/** What a dialog's request, as a mutation, is to the dialog. */
interface DialogRequest {
  /** Whether the request is under way, while which the dialog stays open. */
  isPending: boolean;
  /** Forgets the request's attempt, as its refusal. */
  reset: () => void;
}

/**
 * Returns the onOpenChange of a dialog whose open state setOpen sets, and
 * whose request is request: the dialog stays open while the request is
 * under way, and, once it closes, forgets the request's attempt, so that it
 * opens again without the refusal of the attempt before, and then does
 * onClosed, as to forget the rest of what the dialog was given.
 */
export function closesUnlessPending(
  setOpen: (open: boolean) => void,
  request: DialogRequest,
  onClosed?: () => void,
): (open: boolean) => void {
  return (next) => {
    if (!next && request.isPending) {
      return;
    }
    setOpen(next);
    if (!next) {
      request.reset();
      onClosed?.();
    }
  };
}

/**
 * Returns the onOpenChange of a dialog that is open for as long as the page
 * shows it, as one that a menu's item opens, and closes by onClose, which
 * stops showing it, and with it its request, whose attempt goes with it: as
 * closesUnlessPending's, it stays open while the request is under way
 * (pending), if it has one.
 */
export function closesBy(
  onClose: () => void,
  pending = false,
): (open: boolean) => void {
  return (next) => {
    if (!next && !pending) {
      onClose();
    }
  };
}

/**
 * The control that opened a dialog, or what finds it as the dialog closes,
 * as the control of an item that the dialog moved, which the page shows
 * anew where the item went.
 */
export type Opener =
  | HTMLElement
  | null
  | undefined
  | (() => HTMLElement | null);

/**
 * Returns the onCloseAutoFocus of a dialog without a Dialog.Trigger, as one
 * that a menu's item opens, or one that outlives the control that opened
 * it: the focus goes back to opener, the control that opened the dialog, or
 * to the page's content once opener has gone, as with its list's item,
 * rather than to the document's body.
 */
export function focusBackTo(opener: Opener) {
  return (event: Event) => {
    event.preventDefault();
    const control = typeof opener === "function" ? opener() : opener;
    if (control?.isConnected) {
      control.focus();
    } else {
      focusContentIfLost(document);
    }
  };
}
