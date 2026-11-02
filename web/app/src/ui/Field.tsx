// The fields of the app's forms. Each has a visible label, a hint and an
// error that a screen reader reads with it (aria-describedby), and is at
// least 44 pixels high, the size of a fingertip (PRD FR-EDIT-09). An error
// marks its field invalid, or the fieldset of a group of boxes or buttons
// that it is of (data-invalid), and useFocusRefusal moves the focus to the
// first such field, or the group's first control, once for each time that a
// form is refused.

import {
  type ComponentProps,
  type ReactNode,
  type RefObject,
  useEffect,
  useId,
  useRef,
} from "react";

/** The classes of a control's box: its boundary at 3:1 against the page. */
const control =
  "min-h-11 w-full rounded-md border border-border bg-canvas px-3 text-fg";

interface FieldFrameProps {
  id: string;
  label: string;
  hint?: string;
  error?: string;
  children: (describedBy: string | undefined) => ReactNode;
}

/** The label, the hint, and the error around a field's control. */
function FieldFrame({ id, label, hint, error, children }: FieldFrameProps) {
  const hintId = hint === undefined ? undefined : `${id}-hint`;
  const errorId = error === undefined ? undefined : `${id}-error`;
  const describedBy =
    [hintId, errorId].filter((s) => s !== undefined).join(" ") || undefined;
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="font-medium">
        {label}
      </label>
      {hint !== undefined && (
        <p id={hintId} className="text-sm text-fg-muted">
          {hint}
        </p>
      )}
      {children(describedBy)}
      {error !== undefined && (
        <p id={errorId} className="text-sm text-danger">
          {error}
        </p>
      )}
    </div>
  );
}

export interface TextFieldProps extends Omit<ComponentProps<"input">, "id"> {
  label: string;
  hint?: string;
  error?: string;
}

/** A field of text, as an email address or a password. */
export function TextField({ label, hint, error, ...input }: TextFieldProps) {
  const id = useId();
  return (
    <FieldFrame id={id} label={label} hint={hint} error={error}>
      {(describedBy) => (
        <input
          id={id}
          className={control}
          aria-invalid={error === undefined ? undefined : true}
          aria-describedby={describedBy}
          {...input}
        />
      )}
    </FieldFrame>
  );
}

export interface SelectFieldProps extends Omit<ComponentProps<"select">, "id"> {
  label: string;
  hint?: string;
  error?: string;
}

/** A choice of one of a list, as a language. */
export function SelectField({
  label,
  hint,
  error,
  ...select
}: SelectFieldProps) {
  const id = useId();
  return (
    <FieldFrame id={id} label={label} hint={hint} error={error}>
      {(describedBy) => (
        <select
          id={id}
          className={control}
          aria-invalid={error === undefined ? undefined : true}
          aria-describedby={describedBy}
          {...select}
        />
      )}
    </FieldFrame>
  );
}

export interface CheckboxFieldProps
  extends Omit<ComponentProps<"input">, "id" | "type"> {
  label: string;
  hint?: string;
}

/** A box to tick, with its label beside it, which ticks it too. */
export function CheckboxField({ label, hint, ...input }: CheckboxFieldProps) {
  const id = useId();
  const hintId = hint === undefined ? undefined : `${id}-hint`;
  return (
    <div className="flex flex-col">
      <label htmlFor={id} className="flex min-h-11 items-center gap-3">
        <input
          id={id}
          type="checkbox"
          className="size-5 shrink-0 accent-accent"
          aria-describedby={hintId}
          {...input}
        />
        <span>{label}</span>
      </label>
      {hint !== undefined && (
        <p id={hintId} className="ms-8 text-sm text-fg-muted">
          {hint}
        </p>
      )}
    </div>
  );
}

/**
 * The selector of what useFocusRefusal moves the focus to: a field marked
 * invalid, or a control of a fieldset whose error is of the whole group, as
 * of boxes to tick, since ARIA has no aria-invalid of a group.
 */
const refusedField = '[aria-invalid="true"], fieldset[data-invalid] input';

/** The selector of a control that the focus can go to. */
const enabledControl =
  "a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled)";

/**
 * Moves the focus to the first field of form that is marked invalid, once
 * for each refused attempt, as a form that the server refused shows its
 * errors, so that a keyboard or screen-reader user lands on what to mend.
 * attempt tells the attempt apart from the one before: the error that the
 * form's mutation failed with, which stays the same object until the next
 * attempt, or a mark of a submission that the form refused itself, as of
 * passwords that differ, made anew for each, or null or undefined if the
 * last attempt was not refused. Whatever else shows the form again, as the
 * user ticking a box of it, leaves the focus where it is, and so does a
 * refusal of the whole form, whose alert is read out, unless the focus is
 * nowhere, as when the control that made the attempt went with the step
 * that showed it, and form, the step that replaced it, shows the refusal:
 * the focus goes to form's first control then, rather than to the top of
 * the page, where the next Tab would start again. form need not be a
 * form: a section of controls is one too.
 */
export function useFocusRefusal(
  form: RefObject<HTMLElement | null>,
  attempt: object | null | undefined,
): void {
  const focused = useRef<WeakSet<object>>(null);
  useEffect(() => {
    focused.current ??= new WeakSet();
    if (attempt == null || focused.current.has(attempt)) {
      return;
    }
    focused.current.add(attempt);
    const within = form.current;
    const field = within?.querySelector<HTMLElement>(refusedField);
    const doc = within?.ownerDocument;
    if (field) {
      field.focus();
    } else if (
      doc &&
      (doc.activeElement === null || doc.activeElement === doc.body)
    ) {
      within?.querySelector<HTMLElement>(enabledControl)?.focus();
    }
  }, [form, attempt]);
}
