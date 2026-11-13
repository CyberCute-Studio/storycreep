// The guided picker of a condition (PRD FR-EXPR-01 AC3, plan note N74):
// what to test, of the values that the board reads, its variables, the
// global ones, and the components' runtime attributes, each named as the
// board's expressions name it; how to test it, by its type; and what to
// test it against, a literal of its type, an enum's option, or a bool. It
// shows the source that the text view shows (picker.ts), which it writes
// anew only as one of its fields is changed, and says so of a source that
// it cannot show, which the text view edits.

import { type RefObject, useEffect, useMemo, useState } from "react";
import { valueNamer } from "../content/textNames";
import { Button } from "../ui/Button";
import { SelectField, TextField } from "../ui/Field";
import type { Analysis } from "./analyze";
import { typeText, type Words } from "./messages";
import {
  defaultValue,
  isNumber,
  type Picked,
  readPicked,
  stringLiteral,
  stringText,
  type Target,
  type TargetType,
  type Test,
  takesValue,
  testsOf,
  writePicked,
} from "./picker";
import { boardVariables, runtimeAttributes, type Scope } from "./scope";

/** A value that the picker offers to test, with what names it and its type. */
interface Choice {
  readonly target: Target;
  readonly value: string;
  readonly name: string;
  readonly group: "board" | "global" | "attribute";
  readonly type: TargetType;
}

/** Returns the value of the choice of target in the picker's list. */
function targetValue(target: Target): string {
  return `${target.ref} ${target.attribute ?? ""}`;
}

/** What orders the choices by their names, made once. */
const collator = new Intl.Collator();

/** Returns the values that the board of scope reads, which the picker offers to test, but text attributes, in the order of their names. */
function choicesOf(scope: Scope): Choice[] {
  const { view, boardId } = scope;
  const name = valueNamer(view, boardId);
  const out: Choice[] = [];
  for (const v of boardVariables(view, boardId)) {
    const target = { ref: v.id };
    out.push({
      target,
      value: targetValue(target),
      name: name(v.id, undefined) ?? v.key,
      group: v.scope === "board" ? "board" : "global",
      type: { kind: v.type },
    });
  }
  for (const c of view.components.values()) {
    for (const a of runtimeAttributes(view, c)) {
      if (a.type === "text") {
        continue;
      }
      const target = { ref: c.id, attribute: a.id };
      out.push({
        target,
        value: targetValue(target),
        name: name(c.id, a.id) ?? `${c.key}.${a.key}`,
        group: "attribute",
        type:
          a.type === "enum"
            ? { kind: "enum", options: a.options ?? [] }
            : { kind: a.type },
      });
    }
  }
  return out.sort((a, b) => collator.compare(a.name, b.name));
}

/** Returns what names a test, of a value of type. */
function testText(w: Words, test: Test): string {
  const { t } = w;
  switch (test) {
    case "is_true":
      return t("expr.picker.is_true");
    case "is_false":
      return t("expr.picker.is_false");
    case "eq":
      return t("expr.picker.eq");
    case "ne":
      return t("expr.picker.ne");
    case "lt":
      return t("expr.picker.lt");
    case "lte":
      return t("expr.picker.lte");
    case "gt":
      return t("expr.picker.gt");
    case "gte":
      return t("expr.picker.gte");
  }
}

/** Reports whether a value of the type a may keep its test and value as one of the type b: of the same kind, and of an enum, of the same options. */
function sameType(a: TargetType, b: TargetType): boolean {
  if (a.kind === "enum" || b.kind === "enum") {
    return (
      a.kind === "enum" &&
      b.kind === "enum" &&
      a.options.length === b.options.length &&
      a.options.every((o, i) => o === b.options[i])
    );
  }
  const numeric = (k: string) => k === "int" || k === "float";
  return a.kind === b.kind || (numeric(a.kind) && numeric(b.kind));
}

/**
 * The guided picker of the condition that analysis is of, of the board of
 * scope, which tells of a change of what it shows by onSource, with the
 * source that it shows, of which no field holds what it cannot write then,
 * and by onUnfinished whether its number's field shows what is not a number
 * yet, which it cannot write, as that changes: a group of fields, which the
 * editor's label names (labelledBy), and which the editor gives the focus to
 * (group), as once a variable is created inline, the same element whether
 * the picker shows the condition or says that it cannot.
 */
export function GuidedPicker({
  group,
  labelledBy,
  analysis,
  scope,
  words,
  describedBy,
  onSource,
  onUnfinished,
}: {
  group: RefObject<HTMLFieldSetElement | null>;
  labelledBy: string;
  analysis: Analysis;
  scope: Scope;
  words: Words;
  describedBy: string | undefined;
  onSource: (source: string) => void;
  onUnfinished: (unfinished: boolean) => void;
}) {
  const { t } = words;
  // What the picker wrote last, which it shows still while the source is
  // what it wrote, though that does not compile, as of a text longer than a
  // string may be, or the page has not read the board's names yet to tell,
  // rather than take the field that is being edited away.
  const [written, setWritten] = useState<{ source: string; picked: Picked }>();
  const picked =
    readPicked(analysis) ??
    (written?.source === analysis.source ? written.picked : undefined);
  const choices = useMemo(() => choicesOf(scope), [scope]);
  // What is typed of a number, which writes the source once it is one, and
  // the field says what it lacks until then, which the form's Save is
  // refused for (onUnfinished), rather than save the number before it; or
  // of a text, which writes the source as it is typed.
  const [draft, setDraft] = useState<{ for: string; text: string }>();
  const current =
    picked?.target === undefined
      ? undefined
      : choices.find((c) => c.value === targetValue(picked.target as Target));
  const type = current?.type;
  const draftKey =
    picked === undefined
      ? ""
      : `${picked.target === undefined ? "" : targetValue(picked.target)} ${picked.test}`;
  const numberProblem =
    picked !== undefined &&
    takesValue(picked.test) &&
    (type?.kind === "int" || type?.kind === "float") &&
    draft?.for === draftKey &&
    !isNumber(draft.text.trim())
      ? t("expr.picker.number_error")
      : undefined;
  // The form's Save is refused while the number's field says what it lacks,
  // and only while it is there to say it: not once the picker cannot show
  // the condition, or its value to test, as once another session took that
  // to the trash, rather than refuse it with nothing to tell why.
  const unfinished = numberProblem !== undefined;
  useEffect(() => onUnfinished(unfinished), [unfinished, onUnfinished]);
  if (picked === undefined) {
    // A condition that parses, of a page that has not read the board's
    // names, or whose read of them failed, may be one that the picker
    // shows, once it can tell (Analysis' checked), which it says, rather
    // than offer to clear it.
    const unchecked = !analysis.checked && analysis.parsed !== undefined;
    return (
      <fieldset
        ref={group}
        tabIndex={-1}
        aria-labelledby={labelledBy}
        aria-describedby={describedBy}
        className="space-y-2 rounded-md border border-border p-3"
      >
        <p>
          {unchecked ? t("expr.picker.unchecked") : t("expr.picker.unshown")}
        </p>
        {!unchecked && (
          <Button
            onClick={() => {
              onSource("");
              // The group keeps the focus, of the button that goes, whose
              // fields to build a condition with follow it.
              group.current?.focus();
            }}
          >
            {t("expr.picker.start_over")}
          </Button>
        )}
      </fieldset>
    );
  }
  const name = valueNamer(scope.view, scope.boardId);
  const nameOf = (target: Target) =>
    name(target.ref, target.attribute) ?? target.ref;
  const writeSource = (next: Picked) => {
    const source = writePicked(next, nameOf);
    setWritten({ source, picked: next });
    onSource(source);
  };
  const write = (next: Picked) => {
    setDraft(undefined);
    writeSource(next);
  };
  const numberText = draft?.for === draftKey ? draft.text : picked.value;
  const chooseTarget = (value: string) => {
    const choice = choices.find((c) => c.value === value);
    if (choice === undefined) {
      write({ test: "eq", value: "" });
      return;
    }
    const keep =
      type !== undefined &&
      sameType(type, choice.type) &&
      testsOf(choice.type).includes(picked.test);
    write(
      keep
        ? { ...picked, target: choice.target }
        : {
            target: choice.target,
            test: choice.type.kind === "bool" ? "is_true" : "eq",
            value: choice.type.kind === "bool" ? "" : defaultValue(choice.type),
          },
    );
  };
  const chooseTest = (test: Test) => {
    if (type === undefined) {
      return;
    }
    write({
      ...picked,
      test,
      value: !takesValue(test)
        ? ""
        : takesValue(picked.test) && picked.value !== ""
          ? picked.value
          : defaultValue(type),
    });
  };
  const valueField = () => {
    if (type === undefined || !takesValue(picked.test)) {
      return null;
    }
    const label = t("expr.picker.value");
    switch (type.kind) {
      case "bool":
        return (
          <SelectField
            label={label}
            value={picked.value}
            onChange={(e) => write({ ...picked, value: e.currentTarget.value })}
          >
            {picked.value !== "true" && picked.value !== "false" && (
              <option value={picked.value}>{picked.value}</option>
            )}
            <option value="true">{t("expr.plain.true")}</option>
            <option value="false">{t("expr.plain.false")}</option>
          </SelectField>
        );
      case "enum": {
        const text = stringText(picked.value);
        const option = text !== undefined && type.options.includes(text);
        return (
          <SelectField
            label={label}
            // An option that the source spells otherwise, as with an
            // escape, is the option still, which the list shows chosen.
            value={option ? stringLiteral(text) : picked.value}
            onChange={(e) => write({ ...picked, value: e.currentTarget.value })}
          >
            {!option && (
              <option value={picked.value}>
                {t("expr.picker.not_an_option", {
                  value: text ?? picked.value,
                })}
              </option>
            )}
            {type.options.map((option) => (
              <option key={option} value={stringLiteral(option)}>
                {option}
              </option>
            ))}
          </SelectField>
        );
      }
      case "string":
        // What is typed, which the field shows as it is while the string
        // that it writes is one that the lexer refuses, as one longer than
        // a string may be, of which the editor says so.
        return (
          <TextField
            label={label}
            autoComplete="off"
            spellCheck={false}
            value={
              draft?.for === draftKey
                ? draft.text
                : (stringText(picked.value) ?? "")
            }
            onChange={(e) => {
              const text = e.currentTarget.value;
              writeSource({ ...picked, value: stringLiteral(text) });
              setDraft({ for: draftKey, text });
            }}
          />
        );
    }
    // The on-screen keyboard is the one of text, whose digits have the minus
    // too: a keyboard of digits alone, as inputmode's numeric and decimal
    // give a phone or iPadOS, may have none (ValueField).
    return (
      <TextField
        label={label}
        autoComplete="off"
        spellCheck={false}
        value={numberText}
        error={numberProblem}
        onChange={(e) => {
          const text = e.currentTarget.value;
          setDraft({ for: draftKey, text });
          if (isNumber(text.trim())) {
            writeSource({ ...picked, value: text.trim() });
          }
        }}
      />
    );
  };
  const groups: { group: Choice["group"]; label: string }[] = [
    { group: "board", label: t("expr.picker.board_variables") },
    { group: "global", label: t("expr.picker.global_variables") },
    { group: "attribute", label: t("expr.picker.attributes") },
  ];
  return (
    <fieldset
      ref={group}
      tabIndex={-1}
      aria-labelledby={labelledBy}
      aria-describedby={describedBy}
      className="space-y-3 rounded-md border border-border p-3"
      data-picker=""
    >
      <SelectField
        label={t("expr.picker.target_label")}
        value={picked.target === undefined ? "" : targetValue(picked.target)}
        onChange={(e) => chooseTarget(e.currentTarget.value)}
      >
        <option value="">{t("expr.picker.choose")}</option>
        {picked.target !== undefined && current === undefined && (
          <option value={targetValue(picked.target)}>
            {t("expr.picker.unread_target")}
          </option>
        )}
        {groups.map(({ group, label }) => {
          const of = choices.filter((c) => c.group === group);
          return of.length === 0 ? null : (
            <optgroup key={group} label={label}>
              {of.map((c) => (
                <option key={c.value} value={c.value}>
                  {t("expr.picker.target", {
                    name: c.name,
                    type: typeText(t, c.type.kind),
                  })}
                </option>
              ))}
            </optgroup>
          );
        })}
      </SelectField>
      {type !== undefined && (
        <SelectField
          label={t("expr.picker.test")}
          value={picked.test}
          onChange={(e) => chooseTest(e.currentTarget.value as Test)}
        >
          {testsOf(type).map((test) => (
            <option key={test} value={test}>
              {testText(words, test)}
            </option>
          ))}
        </SelectField>
      )}
      {valueField()}
    </fieldset>
  );
}
