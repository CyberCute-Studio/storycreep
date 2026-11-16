// Play mode's variable inspector (PRD FR-PLAY-02, plan note N79): every
// variable and every runtime attribute of the snapshot that play runs, with
// its value as the flow holds it now, grouped as valueGroups groups them,
// which a filter narrows by their names, and those that the last step
// changed marked, in words and by the play colour (AC2). Each has the field
// of its type, a box for a bool, a choice of the options for an enum, and
// a field of text for a number or a string, which takes only what is a
// value of the type, and says why it does not take the rest (AC3); the
// value set goes into the flow at once, as its next evaluation reads it,
// without a restart (AC1). Escape in its filter or in a value's field
// empties the filter or goes back to the value, and never closes the sheet
// that the panel is, which would stop play; and a turn, as a choice, a step
// back, or a reset, discards what a field holds unset, so that it shows the
// value that the turn left.

import {
  boolValue,
  type Value,
  valueString,
} from "@storycreep/shared/expr/compile";
import { type KeyboardEvent, memo, useId, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "zustand";
import { typeName, valueError } from "../content/ValueField";
import { inputMethodTook } from "../richtext/suggest";
import {
  CheckboxField,
  SelectField,
  TextAreaField,
  TextField,
} from "../ui/Field";
import { foldWords, wordsMatcher } from "../ui/match";
import { escaped, keepSheetOpen } from "./keys";
import type { ProjectedValue } from "./projection";
import { type PlaySession, type PlayState, steppedSinceStart } from "./session";
import { type GroupNames, typedValue, valueGroups } from "./values";

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
  // Whether the flow has a step to step back from, beyond the start's, as
  // the panel's Step back reads it.
  const stepped = steppedSinceStart(flow);
  const matches = wordsMatcher(filter);
  // The words of each value's name, folded once for the values that play
  // runs, rather than on each draw of the panel, which each step, each
  // value set, and each letter typed in the filter makes: of a thousand
  // values, folding them all takes some milliseconds each time, many times
  // what grouping them does.
  const words = useMemo(
    () => new Map(projection.values.map((v) => [v.key, foldWords(v.name)])),
    [projection.values],
  );
  const groups = valueGroups(t, projection.values, names)
    .map((g) => {
      const groupWords = foldWords(g.name);
      return {
        ...g,
        values: g.values.filter((v) =>
          matches([...(words.get(v.key) ?? []), ...groupWords]),
        ),
      };
    })
    .filter((g) => g.values.length > 0);
  return (
    // No Escape in the filter or in a value's field closes the sheet that
    // the panel is, which would stop play and lose its history, as one
    // pressed once more than there was to empty or to go back from, or after
    // a value set by Enter, would: each empties the filter or goes back to
    // the value alone (plan note N79), as none in the saved states does
    // (keepSheetOpen).
    <section
      aria-labelledby={titleId}
      className="space-y-3"
      onKeyDown={keepSheetOpen}
    >
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
            // Escape empties the filter, as a search box's does, but for one
            // that cancels what an input method was composing, which is the
            // input method's, and empties no filter (escaped).
            onKeyDown={escaped(() => session.view.setState({ filter: "" }))}
          />
          {/* A flow that has taken no step since its start's, as one that
              halted or ended in it, has no step to step back from, as Step
              back says, being disabled, so that what the inspector says of
              how to go on does not name it. */}
          {flow.halted ? (
            <p className="text-sm">
              {stepped
                ? t("play.inspector.halted")
                : t("play.inspector.halted_at_start")}
            </p>
          ) : (
            flow.phase === "ended" && (
              <p className="text-sm">
                {stepped
                  ? t("play.inspector.ended")
                  : t("play.inspector.ended_at_start")}
              </p>
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
// changes, or a turn discards what its field holds unset (ValueEditor): the
// session keeps a value that a step, a step back, or a value set by hand
// does not change as the same object (session.ts's valuesOf), so that each
// of them, in a project of a thousand variables, draws the rows of those
// that it changed alone.
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

/** What a value's field holds that it has not set (ValueEditor). */
interface Unset {
  /** What the user typed, or undefined where the field shows the value, as of an enum's choice that the runtime refused. */
  readonly draft?: string;
  /** Why the field did not set it, or undefined where it has not tried yet. */
  readonly error?: string;
  /** The session's turn that it was typed or chosen in. */
  readonly turn: number;
}

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
  // What the field holds that it has not set, what the user typed and why
  // the field did not set it, of the turn that it was typed in, or
  // undefined while the field shows the value, as the flow changes it. A
  // turn, as a choice, a step back, or a reset, discards it, so that the
  // field shows the value that the turn left, and its mark; the row reads
  // the turn only while it holds something unset, so that a turn draws
  // again the rows of the values that it changed, and of those alone
  // (ValueRow).
  const [unset, setUnset] = useState<Unset>();
  const turn = useStore(session.store, (s) =>
    unset !== undefined && s.status === "playing" ? s.turn : undefined,
  );
  if (unset !== undefined && unset.turn !== turn) {
    setUnset(undefined);
  }
  const live = unset?.turn === turn ? unset : undefined;
  const draft = live?.draft;
  const error = live?.error;
  // now returns the session's turn, which what the field holds unset is of.
  const now = () => {
    const s = session.state;
    return s.status === "playing" ? s.turn : 0;
  };
  const type = typeName(t, target.type);
  const hint = changed
    ? t("play.inspector.hint", {
        type,
        changed: t("play.inspector.changed"),
      })
    : type;
  // refused sets v, and returns why the runtime does not take it, as a
  // string of more than 4,096 characters, or with a NUL or a lone
  // surrogate, or undefined once it is set.
  const refused = (v: Value): string | undefined =>
    session.edit(target.key, v)
      ? undefined
      : target.type === "string"
        ? t("play.inspector.string_error")
        : valueError(t, target.type);
  // commit sets what text spells, or says that it is no value of the type,
  // and keeps it in the field, unset, until the user mends it, goes back to
  // the value with Escape, or a turn discards it.
  const commit = (text: string) => {
    const v = typedValue(target, text);
    const why = v === undefined ? valueError(t, target.type) : refused(v);
    setUnset(
      why === undefined ? undefined : { draft: text, error: why, turn: now() },
    );
  };
  const keys = (
    event: KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>,
    enterSets: boolean,
  ) => {
    // An Enter that ends what an input method was composing, or an Escape
    // that cancels it, as the event says, or as Safari tells of one, once
    // the composition has ended, by its keyCode alone, is the input
    // method's, which neither sets what is typed, nor goes back to the
    // value, which would lose it (inputMethodTook).
    if (inputMethodTook(event.nativeEvent)) {
      return;
    }
    if (event.key === "Escape") {
      // Escape goes back to the value, and closes no sheet (Inspector).
      setUnset(undefined);
    } else if (event.key === "Enter" && enterSets && draft !== undefined) {
      event.preventDefault();
      commit(draft);
    }
  };
  const typed = {
    label: target.name,
    hint,
    error,
    // A value as to_string writes it, a string as it is.
    value: draft ?? valueString(value),
    autoComplete: "off",
    spellCheck: false,
    onChange: (event: { currentTarget: { value: string } }) => {
      setUnset({ draft: event.currentTarget.value, turn: now() });
    },
    onBlur: () => {
      if (draft !== undefined) {
        commit(draft);
      }
    },
  };
  switch (target.type) {
    case "bool":
      // The runtime takes either value of a bool, so that the session
      // refuses one only once play no longer runs, where the inspector is
      // gone: the box has no error to say, and stays as the value is.
      return (
        <CheckboxField
          label={target.name}
          hint={hint}
          checked={value.bool}
          onChange={(event) =>
            session.edit(target.key, boolValue(event.currentTarget.checked))
          }
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
              const why = refused(v);
              setUnset(
                why === undefined ? undefined : { error: why, turn: now() },
              );
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
