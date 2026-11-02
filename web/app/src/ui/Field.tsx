// The fields of the app's forms. Each has a visible label, a hint and an
// error that a screen reader reads with it (aria-describedby), and is at
// least 44 pixels high, the size of a fingertip (PRD FR-EDIT-09). An error
// marks its field invalid, and useFocusRefusal moves the focus to the first
// such field once a form is refused.

import {
  type ComponentProps,
  type ReactNode,
  type RefObject,
  useEffect,
  useId,
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
 * Moves the focus to the first field of form that is marked invalid, once
 * refusal changes, as a form that the server refused shows its errors, so
 * that a keyboard or screen-reader user lands on what to mend. A refusal of
 * the whole form leaves the focus where it is: its alert is read out.
 */
export function useFocusRefusal(
  form: RefObject<HTMLFormElement | null>,
  refusal: unknown,
): void {
  useEffect(() => {
    if (refusal === undefined) {
      return;
    }
    form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  }, [form, refusal]);
}
