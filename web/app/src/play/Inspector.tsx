// Play mode's variable inspector (PRD FR-PLAY-02, plan note N79): every
// variable and every runtime attribute of the snapshot that play runs, with
// its value as the flow holds it now, grouped as valueGroups groups them,
// which a filter narrows by their names, and those that the last step
// changed marked, in words and by the play colour (AC2). Each has the field
// of its type, a box for a bool, a choice of the options for an enum, and
// a field of text for a number or a string, which takes only what is a
// value of the type, and says why it does not take the rest (AC3); the
// value set goes into the flow at once, as its next evaluation reads it,
// without a restart (AC1).

import { boolValue, type Value } from "@storycreep/shared/expr/compile";
import { type KeyboardEvent, memo, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "zustand";
import { typeName, valueError } from "../content/ValueField";
import {
  CheckboxField,
  SelectField,
  TextAreaField,
  TextField,
} from "../ui/Field";
import { foldWords, wordsMatcher } from "../ui/match";
import type { ProjectedValue } from "./projection";
import type { PlaySession, PlayState } from "./session";
import { fieldText, type GroupNames, typedValue, valueGroups } from "./values";

export function Inspector({
  session,
  state,
  names,
}: {
  session: PlaySession;
  state: Extract<PlayState, { status: "playing" }>;
  names: GroupNames;
}) {
  const { t } = useTranslation();
  const filter = useStore(session.view, (v) => v.filter);
  const titleId = useId();
  const { flow, projection } = state;
  const matches = wordsMatcher(filter);
  const groups = valueGroups(t, projection.values, names)
    .map((g) => ({
      ...g,
      values: g.values.filter((v) =>
        matches([...foldWords(v.name), ...foldWords(g.name)]),
      ),
    }))
    .filter((g) => g.values.length > 0);
  return (
    <section aria-labelledby={titleId} className="space-y-3">
      <h3 id={titleId} className="font-semibold">
        {t("play.inspector.title")}
      </h3>
      {projection.values.length === 0 ? (
        <p className="text-fg-muted">{t("play.inspector.none")}</p>
      ) : (
        <>
          <TextField
            type="search"
            label={t("play.inspector.filter")}
            hint={t("play.inspector.filter_hint")}
            value={filter}
            autoComplete="off"
            spellCheck={false}
            onChange={(event) =>
              session.view.setState({ filter: event.currentTarget.value })
            }
          />
          {flow.halted ? (
            <p className="text-sm">{t("play.inspector.halted")}</p>
          ) : (
            flow.phase === "ended" && (
              <p className="text-sm">{t("play.inspector.ended")}</p>
            )
          )}
          {groups.length === 0 ? (
            <p className="text-fg-muted">{t("play.inspector.no_match")}</p>
          ) : (
            groups.map((g) => (
              <ValueGroupView
                key={g.id}
                heading={g.heading}
                values={g.values}
                session={session}
                current={flow.values}
                changes={flow.changes}
              />
            ))
          )}
        </>
      )}
    </section>
  );
}

function ValueGroupView({
  heading,
  values,
  session,
  current,
  changes,
}: {
  heading: string;
  values: readonly ProjectedValue[];
  session: PlaySession;
  current: ReadonlyMap<string, Value>;
  changes: ReadonlySet<string>;
}) {
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h4 id={headingId} className="text-sm font-semibold text-fg-muted">
        {heading}
      </h4>
      <ul className="space-y-2">
        {values.map((v) => {
          const value = current.get(v.key);
          return (
            value !== undefined && (
              <ValueRow
                key={v.key}
                session={session}
                target={v}
                value={value}
                changed={changes.has(v.key)}
              />
            )
          );
        })}
      </ul>
    </section>
  );
}

// A value's row, which draws again only once its value, or its mark,
// changes: the runtime keeps a value that a step does not set as the same
// object, so that a step of a project of a thousand variables draws the rows
// of those that it changed alone.
const ValueRow = memo(function ValueRow(props: {
  session: PlaySession;
  target: ProjectedValue;
  value: Value;
  changed: boolean;
}) {
  return (
    <li
      className={
        props.changed
          ? "border-s-4 border-play ps-2"
          : "border-s-4 border-transparent ps-2"
      }
    >
      <ValueEditor {...props} />
    </li>
  );
});

function ValueEditor({
  session,
  target,
  value,
  changed,
}: {
  session: PlaySession;
  target: ProjectedValue;
  value: Value;
  /** Whether the last step changed it, which its hint says. */
  changed: boolean;
}) {
  const { t } = useTranslation();
  // What the user typed and has not set yet, or undefined while the field
  // shows the value, as the flow changes it.
  const [draft, setDraft] = useState<string>();
  const [error, setError] = useState<string>();
  const type = typeName(t, target.type);
  const hint = changed
    ? t("play.inspector.hint", {
        type,
        changed: t("play.inspector.changed"),
      })
    : type;
  // set sets v, or says that the runtime does not take it, as a string of
  // more than 4,096 characters, and reports whether it was set.
  const set = (v: Value): boolean => {
    if (!session.edit(target.key, v)) {
      setError(
        target.type === "string"
          ? t("play.inspector.string_error")
          : valueError(t, target.type),
      );
      return false;
    }
    setError(undefined);
    return true;
  };
  // commit sets what text spells, or says that it is no value of the type,
  // and keeps it in the field, unset, until the user mends it or goes back
  // to the value with Escape.
  const commit = (text: string) => {
    const v = typedValue(target, text);
    if (v === undefined) {
      setError(valueError(t, target.type));
      return;
    }
    if (set(v)) {
      setDraft(undefined);
    }
  };
  const keys = (
    event: KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>,
    enterSets: boolean,
  ) => {
    if (event.key === "Escape" && draft !== undefined) {
      // Escape goes back to the value, rather than closing the sheet that
      // the panel is.
      event.stopPropagation();
      setDraft(undefined);
      setError(undefined);
    } else if (event.key === "Enter" && enterSets && draft !== undefined) {
      event.preventDefault();
      commit(draft);
    }
  };
  const typed = {
    label: target.name,
    hint,
    error,
    value: draft ?? fieldText(value),
    autoComplete: "off",
    spellCheck: false,
    onChange: (event: { currentTarget: { value: string } }) => {
      setDraft(event.currentTarget.value);
      setError(undefined);
    },
    onBlur: () => {
      if (draft !== undefined) {
        commit(draft);
      }
    },
  };
  switch (target.type) {
    case "bool":
      return (
        <CheckboxField
          label={target.name}
          hint={hint}
          checked={value.bool}
          onChange={(event) => set(boolValue(event.currentTarget.checked))}
        />
      );
    case "enum":
      return (
        <SelectField
          label={target.name}
          hint={hint}
          error={error}
          value={value.str}
          onChange={(event) => {
            const v = typedValue(target, event.currentTarget.value);
            if (v !== undefined) {
              set(v);
            }
          }}
        >
          {/* A value that is none of the options, which the runtime holds
              none of, shows as it is. */}
          {!target.options.includes(value.str) && (
            <option value={value.str}>{value.str}</option>
          )}
          {target.options.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </SelectField>
      );
    case "string":
      return (
        <TextAreaField
          {...typed}
          rows={2}
          onKeyDown={(event) => keys(event, false)}
        />
      );
  }
  return <TextField {...typed} onKeyDown={(event) => keys(event, true)} />;
}
