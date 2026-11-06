// The field of a typed value (PRD FR-VAR-01, FR-COMP-01), as a variable's
// default or a component's value of an attribute: a choice of true or false
// for a bool, of an option for an enum, a field of digits for a number, and
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
}

/** Returns the message of a value that is none of type, which the field says. */
export function valueError(
  t: ReturnType<typeof useTranslation>["t"],
  type: ValueType,
): string {
  return t("value.error", { type });
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
}: ValueFieldProps) {
  const { t } = useTranslation();
  const common = { label, hint, name, error, disabled };
  switch (type) {
    case "bool":
      return (
        <SelectField {...common} defaultValue={textOf(defaultValue)}>
          <option value="false">{t("value.false")}</option>
          <option value="true">{t("value.true")}</option>
        </SelectField>
      );
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
      return (
        <TextField
          {...common}
          inputMode={type === "int" ? "numeric" : "decimal"}
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
