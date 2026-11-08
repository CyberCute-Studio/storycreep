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
import { focusIsNowhere } from "../shell/focus";

/** The classes of a control's box: its boundary at 3:1 against the page. */
const control =
  "min-h-11 w-full rounded-md border border-border bg-canvas px-3 text-fg";

/** Returns the IDs of the hint and the error of the field whose control is id, and the list of them that the control's aria-describedby takes. */
export function fieldDescription(
  id: string,
  hint: string | undefined,
  error: string | undefined,
): { hintId?: string; errorId?: string; describedBy?: string } {
  const hintId = hint === undefined ? undefined : `${id}-hint`;
  const errorId = error === undefined ? undefined : `${id}-error`;
  const describedBy =
    [hintId, errorId].filter((s) => s !== undefined).join(" ") || undefined;
  return { hintId, errorId, describedBy };
}

interface FieldFrameProps {
  id: string;
  label: string;
  hint?: string;
  error?: string;
  /**
   * Focuses a control that is no form control, which a click on its label
   * does not focus, as a rich text's editable element: its label's ID is
   * then labelId, which its aria-labelledby names, as a label names only a
   * form control.
   */
  focus?: { labelId: string; onLabelClick: () => void };
  children: (describedBy: string | undefined) => ReactNode;
}

/** The label, the hint, and the error around a field's control. */
export function FieldFrame({
  id,
  label,
  hint,
  error,
  focus,
  children,
}: FieldFrameProps) {
  const { hintId, errorId, describedBy } = fieldDescription(id, hint, error);
  return (
    <div className="flex flex-col gap-1">
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: a label takes no focus, and a keyboard goes to its control, as it does every field's. */}
      <label
        htmlFor={id}
        id={focus?.labelId}
        onClick={focus?.onLabelClick}
        className="font-medium"
      >
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

export interface TextAreaFieldProps
  extends Omit<ComponentProps<"textarea">, "id"> {
  label: string;
  hint?: string;
  error?: string;
}

/** A field of text of more than one line, as a description. */
export function TextAreaField({
  label,
  hint,
  error,
  ...textarea
}: TextAreaFieldProps) {
  const id = useId();
  return (
    <FieldFrame id={id} label={label} hint={hint} error={error}>
      {(describedBy) => (
        <textarea
          id={id}
          rows={3}
          className={`${control} py-2`}
          aria-invalid={error === undefined ? undefined : true}
          aria-describedby={describedBy}
          {...textarea}
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
export function CheckboxField(props: CheckboxFieldProps) {
  return <ChoiceField type="checkbox" {...props} />;
}

/** A box to tick or a button to choose, with its label beside it. */
function ChoiceField({
  label,
  hint,
  type,
  ...input
}: CheckboxFieldProps & { type: "checkbox" | "radio" }) {
  const id = useId();
  const hintId = hint === undefined ? undefined : `${id}-hint`;
  return (
    <div className="flex flex-col">
      <label htmlFor={id} className="flex min-h-11 items-center gap-3">
        <input
          id={id}
          type={type}
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

export interface RadioFieldProps
  extends Omit<ComponentProps<"input">, "id" | "type"> {
  label: string;
  hint?: string;
}

/** A button to choose one of a GroupField's choices, with its label beside it. */
export function RadioField(props: RadioFieldProps) {
  return <ChoiceField type="radio" {...props} />;
}

export interface GroupFieldProps {
  legend: string;
  /** The classes of the legend, a field's label's unless they say otherwise. */
  legendClassName?: string;
  className?: string;
  /** The error of the whole group, as of none of its boxes ticked. */
  error?: string;
  children: ReactNode;
}

/**
 * A group of boxes to tick or buttons to choose one of, as a token's
 * scopes, with its legend, and its error, which is the whole group's: its
 * fieldset says data-invalid then, as ARIA has no aria-invalid of a group,
 * and useFocusRefusal moves the focus to its first control.
 */
export function GroupField({
  legend,
  legendClassName = "font-medium",
  className,
  error,
  children,
}: GroupFieldProps) {
  const errorId = useId();
  return (
    <fieldset
      className={className}
      aria-describedby={error === undefined ? undefined : errorId}
      data-invalid={error === undefined ? undefined : true}
    >
      <legend className={legendClassName}>{legend}</legend>
      {children}
      {error !== undefined && (
        <p id={errorId} className="text-sm text-danger">
          {error}
        </p>
      )}
    </fieldset>
  );
}

/**
 * The selector of what useFocusRefusal moves the focus to: a field marked
 * invalid, or a control of a group whose error is the whole group's
 * (GroupField).
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
 * refusal of the whole form, whose alert is read out, unless no control
 * has the focus (focusIsNowhere), as when the control that made the attempt
 * went with the step that showed it, and form, the step that replaced it,
 * shows the refusal: the focus goes to form's first control then, rather
 * than to the page's content, or its top, where the next Tab would start
 * again. form need not be a form: a section of controls is one too.
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
    } else if (doc && focusIsNowhere(doc)) {
      within?.querySelector<HTMLElement>(enabledControl)?.focus();
    }
  }, [form, attempt]);
}
