// The field of a typed value (PRD FR-VAR-01, FR-COMP-01), as a variable's
// default or a component's value of an attribute: a choice of true or false
// for a bool, of an option for an enum, a field of one line for a number, and
// of text for a string, of several lines for a text. The page reads what it
// holds by its name, and parses it with parseValue, which says what of it is
// none of the type before anything is sent.

import { useTranslation } from "react-i18next";
import { SelectField, TextAreaField, TextField } from "../ui/Field";
import { textOf, type Value, type ValueType } from "./values";

export interface ValueFieldProps {
  label: string;
  hint?: string;
  name: string;
  type: ValueType;
  /** An enum's options, which its choice offers. */
  options?: readonly string[];
  /** The value that the field starts with; the page keys it by its type, so that another type shows its own. */
  defaultValue: Value;
  error?: string;
  disabled?: boolean;
  /** Called with what the field holds as the user changes it. */
  onChange?: (text: string) => void;
}

/** Returns the message of a value that is none of type, which the field says. */
export function valueError(
  t: ReturnType<typeof useTranslation>["t"],
  type: ValueType,
): string {
  return t("value.error", { type });
}

/** Returns the name of a value's type, as a variable's or an attribute definition's, in the page's locale. */
export function typeName(
  t: ReturnType<typeof useTranslation>["t"],
  type: string,
): string {
  return t("value.type", { type });
}

/** Returns the text that a page shows of value where no field holds it, as a viewer's panel: a bool as its field's choice names it, in the page's locale. */
export function valueText(
  t: ReturnType<typeof useTranslation>["t"],
  value: Value,
): string {
  return typeof value === "boolean"
    ? value
      ? t("value.true")
      : t("value.false")
    : textOf(value);
}

export function ValueField({
  label,
  hint,
  name,
  type,
  options = [],
  defaultValue,
  error,
  disabled,
  onChange,
}: ValueFieldProps) {
  const { t } = useTranslation();
  const common = {
    label,
    hint,
    name,
    error,
    disabled,
    onChange:
      onChange === undefined
        ? undefined
        : (event: { currentTarget: { value: string } }) =>
            onChange(event.currentTarget.value),
  };
  switch (type) {
    case "bool": {
      const value = textOf(defaultValue);
      return (
        <SelectField {...common} defaultValue={value}>
          {/* A value that is no bool, as one that a form holds of an
              attribute that became a bool elsewhere, shows as it is until
              the user chooses another, as of an enum. */}
          {value !== "false" && value !== "true" && (
            <option value={value}>{value}</option>
          )}
          <option value="false">{t("value.false")}</option>
          <option value="true">{t("value.true")}</option>
        </SelectField>
      );
    }
    case "enum": {
      const value = textOf(defaultValue);
      return (
        <SelectField {...common} defaultValue={value}>
          {/* A value that is none of the options, as of a change of them
              elsewhere, shows as it is until the user chooses another. */}
          {!options.includes(value) && <option value={value}>{value}</option>}
          {options.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </SelectField>
      );
    }
    case "int":
    case "float":
      // The on-screen keyboard is the one of text, whose digits have the
      // minus too: a keyboard of digits alone, as inputmode's numeric and
      // decimal give a phone, may have none, and a number may be negative.
      return (
        <TextField
          {...common}
          autoComplete="off"
          spellCheck={false}
          defaultValue={textOf(defaultValue)}
        />
      );
    case "text":
      return (
        <TextAreaField
          {...common}
          rows={6}
          defaultValue={textOf(defaultValue)}
        />
      );
  }
  return (
    <TextAreaField {...common} rows={2} defaultValue={textOf(defaultValue)} />
  );
}
