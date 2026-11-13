// The expression editor (PRD FR-EXPR-01, FR-VAR-02 AC1, FR-DEP-01 AC3,
// plan note N74): a condition's, of one line, or a script's, of several,
// whose text a text area holds, with what it is, coloured, under it, as the
// expression language reads it, and its first error marked at the range of
// characters that the parser or the checker gives it, which it says under
// the field too; what it offers to complete at the caret, of the names that
// are valid at the expression's board, each with its type, what it is, and,
// of a variable, where it is written; each name that the expression
// resolves, with what it names, of which scope, and its usages and its
// panel a press away; what it says in plain language; an offer to create a
// variable of a name that names nothing; and, of a condition, the guided
// picker, another view of the same source (GuidedPicker.tsx).

import type { Expression } from "@storycreep/shared/api";
import type { Context } from "@storycreep/shared/expr/compile";
import { useQuery } from "@tanstack/react-query";
import {
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { api } from "../api";
import { pressedClass } from "../ui/Button";
import { type Analysis, analyze, type Run, runsOf, unitRange } from "./analyze";
import { editOf, type Offer, offersAt, type Spot, spotAt } from "./complete";
import { GuidedPicker } from "./GuidedPicker";
import { errorText, type SourceError, type Words } from "./messages";
import { Names, PlainText, UnknownName } from "./Names";
import type { Scope } from "./scope";
import { useScope } from "./useScope";

/** What the editor of an expression is given. */
export interface ExpressionEditorProps {
  projectId: string;
  /** The board of the expression's owner, whose names it reads. */
  boardId: string;
  /** Whether it is a condition, of one line, or a script. */
  context: Context;
  label: string;
  hint?: string;
  /** The source that it shows first. */
  defaultValue: string;
  /** The expression as its owner holds it, whose first error, as the server compiled it, the editor shows while it shows that source and finds none itself. */
  stored?: Expression;
  /** What a refusal of the form's save says of the field. */
  error?: string;
  /** Whether the empty source is no expression, as a connection's condition and a script are, rather than one that does not compile, as a branch output's condition. */
  optional: boolean;
  /** Whether the guided picker is offered, of a condition. */
  guided?: boolean;
  /** Called with the source as it changes. */
  onChange: (source: string) => void;
}

/** The classes of each tint of what the editor colours (analyze.ts), each at 4.5:1 against the field (tokens.test.ts). */
const tints: Record<Run["tint"], string> = {
  plain: "",
  keyword: "text-code-keyword",
  literal: "text-code-number",
  number: "text-code-number",
  string: "text-code-string",
  comment: "text-fg-muted",
  name: "text-accent",
  function: "text-code-keyword",
};

/**
 * Returns what the overlay of a text area shows of analysis: each run of its
 * source in its tint, and the range of err, in UTF-16 code units, marked
 * (data-error), a range that covers no character as a bar between two, or
 * at the end.
 */
function tinted(
  analysis: Analysis,
  err: { from: number; to: number } | undefined,
): ReactNode[] {
  const { source } = analysis;
  const out: ReactNode[] = [];
  const marker = (at: number) => (
    <span key={`marker-${at}`} data-error="" className="relative">
      <span className="absolute inset-y-0 -start-px w-0.5 bg-danger" />
    </span>
  );
  let markerShown = err === undefined;
  for (const run of runsOf(analysis)) {
    const cuts = [run.from, run.to];
    if (err !== undefined) {
      for (const at of [err.from, err.to]) {
        if (at > run.from && at < run.to) {
          cuts.push(at);
        }
      }
    }
    cuts.sort((a, b) => a - b);
    for (let i = 0; i + 1 < cuts.length; i++) {
      const from = cuts[i];
      const to = cuts[i + 1];
      if (
        !markerShown &&
        err !== undefined &&
        err.from === err.to &&
        from === err.from
      ) {
        out.push(marker(from));
        markerShown = true;
      }
      const inError =
        err !== undefined &&
        err.from < err.to &&
        from >= err.from &&
        to <= err.to;
      out.push(
        <span
          key={`${from}-${to}`}
          className={`${tints[run.tint]} ${
            inError
              ? "underline decoration-danger decoration-wavy decoration-2"
              : ""
          }`}
          {...(inError ? { "data-error": "" } : {})}
        >
          {source.slice(from, to)}
        </span>,
      );
    }
  }
  if (!markerShown && err !== undefined) {
    out.push(marker(err.from));
  }
  return out;
}

/** The keys that the list of offers takes while it is open. */
const listKeys: ReadonlySet<string> = new Set([
  "ArrowDown",
  "ArrowUp",
  "Enter",
  "Tab",
  "Escape",
]);

/** Writes text in place of from to to of the text area, as typing it would, so that the text area's own undo undoes it, and puts the caret at caret. */
function insertInto(
  area: HTMLTextAreaElement,
  edit: { from: number; to: number; text: string; caret: number },
): void {
  area.focus();
  area.setSelectionRange(edit.from, edit.to);
  const expected =
    area.value.slice(0, edit.from) + edit.text + area.value.slice(edit.to);
  let typed = false;
  try {
    typed =
      typeof document.execCommand === "function" &&
      document.execCommand("insertText", false, edit.text);
  } catch {
    typed = false;
  }
  if (!typed && area.value !== expected) {
    // Where the browser has no such command, as a test's, the text is
    // written as a change of the field, which the page hears as typing.
    area.setRangeText(edit.text, edit.from, edit.to, "end");
    area.dispatchEvent(new Event("input", { bubbles: true }));
  }
  area.setSelectionRange(edit.caret, edit.caret);
}

/** Where the variable of id is written, as the first sites that its usages list of its writes, which the highlighted offer tells of. */
function Writes({ projectId, id }: { projectId: string; id: string }) {
  const { t } = useTranslation();
  const writes = useQuery({
    queryKey: ["projects", projectId, "variables", id, "writes", "first"],
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "listVariableUsages",
          {
            path: { project_id: projectId, variable_id: id },
            query: { access: "write", limit: 3 },
          },
          { signal },
        )
      ).body,
    // A refusal says nothing of its own here: the offer says that where it
    // is written could not be read.
    meta: { showsRefusals: true },
    staleTime: 30_000,
  });
  if (writes.isPending) {
    return (
      <span className="text-fg-muted">{t("expr.offer.writes_loading")}</span>
    );
  }
  if (writes.isError) {
    return (
      <span className="text-fg-muted">{t("expr.offer.writes_failed")}</span>
    );
  }
  const sites = writes.data.items.map((usage) =>
    usage.board_id === undefined
      ? t("expr.offer.write_site_unboarded")
      : t("expr.offer.write_site", {
          board: usage.board_name ?? t("boards.unknown_board"),
          node:
            usage.node_title === undefined || usage.node_title === ""
              ? t("boards.untitled_node")
              : usage.node_title,
        }),
  );
  return (
    <span className="text-fg-muted">
      {sites.length === 0
        ? t("expr.offer.written_nowhere")
        : writes.data.next_cursor === null
          ? t("expr.offer.written_in", { sites: sites.join("; ") })
          : t("expr.offer.written_in_more", { sites: sites.join("; ") })}
    </span>
  );
}

/** What tells one spot from another, of which an Escape closes the list until the caret leaves it. */
function spotKey(spot: Spot): string {
  return `${spot.kind} ${spot.from}`;
}

/**
 * The text view of an expression: the text area, the colours under it, and
 * the list of what it offers to complete at the caret, below it, while it
 * has the focus. The text area keeps the focus, as a combobox's pattern has
 * it: its arrows move through the list (aria-activedescendant), Enter and
 * Tab write what is highlighted, Escape closes the list until the caret
 * leaves what it was of, and a click or a tap on an offer writes it without
 * taking the focus, so that the on-screen keyboard stays. Enter elsewhere
 * in a condition, which is one line, saves its form, as Enter in a field of
 * one line does.
 */
function TextView({
  id,
  projectId,
  analysis,
  scope,
  words,
  describedBy,
  err,
  onSource,
  area,
}: {
  id: string;
  projectId: string;
  analysis: Analysis;
  scope: Scope;
  words: Words;
  describedBy: string | undefined;
  /** The range of the error that the field shows, in UTF-16 code units, if it shows one. */
  err: { from: number; to: number } | undefined;
  onSource: (source: string) => void;
  area: RefObject<HTMLTextAreaElement | null>;
}) {
  const { t } = words;
  const listId = useId();
  const { source, context } = analysis;
  const [caret, setCaret] = useState<number>();
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState<string>();
  const [moved, setMoved] = useState<{ at: string; offer: string }>();
  const spot = useMemo(
    () => (caret === undefined ? undefined : spotAt(source, caret)),
    [source, caret],
  );
  const open =
    focused && spot !== undefined && dismissed !== spotKey(spot)
      ? spot
      : undefined;
  const { offers, total } = useMemo(
    () =>
      open === undefined
        ? { offers: [] as Offer[], total: 0 }
        : offersAt(open, context, scope.view, scope.boardId, words),
    [open, context, scope, words],
  );
  const shown = offers.length > 0 ? open : undefined;
  const at = shown === undefined ? "" : spotKey(shown);
  const index =
    shown === undefined
      ? -1
      : Math.max(
          0,
          moved?.at === at ? offers.findIndex((o) => o.id === moved.offer) : 0,
        );
  const optionId = (i: number) => `${listId}-${i}`;
  const readCaret = (el: HTMLTextAreaElement) => {
    const next =
      el.selectionStart === el.selectionEnd ? el.selectionStart : undefined;
    setCaret(next);
    // An Escape closes the list of what the caret was in alone.
    const nextSpot = next === undefined ? undefined : spotAt(el.value, next);
    if (
      dismissed !== undefined &&
      (nextSpot === undefined || spotKey(nextSpot) !== dismissed)
    ) {
      setDismissed(undefined);
    }
  };
  const choose = (offer: Offer, where: Spot) => {
    const el = area.current;
    if (el === null) {
      return;
    }
    insertInto(el, editOf(el.value, where, offer));
    readCaret(el);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    const composing =
      event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229;
    if (composing) {
      return;
    }
    const plainKey =
      !event.shiftKey && !event.altKey && !event.ctrlKey && !event.metaKey;
    if (shown !== undefined && plainKey && listKeys.has(event.key)) {
      event.preventDefault();
      const moveTo = (i: number) => setMoved({ at, offer: offers[i].id });
      switch (event.key) {
        case "ArrowDown":
          moveTo((index + 1) % offers.length);
          break;
        case "ArrowUp":
          moveTo((index - 1 + offers.length) % offers.length);
          break;
        case "Escape":
          setDismissed(at);
          break;
        default:
          choose(offers[index], shown);
      }
      return;
    }
    if (event.key === "Enter" && plainKey && context === "condition") {
      event.preventDefault();
      event.currentTarget.form?.requestSubmit();
    }
  };
  return (
    <>
      <div className="expr-field rounded-md border border-border bg-canvas">
        <pre aria-hidden="true">
          {tinted(analysis, err)}
          {"​"}
        </pre>
        <textarea
          ref={area}
          id={id}
          value={source}
          rows={1}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
          aria-invalid={err !== undefined || undefined}
          aria-describedby={describedBy}
          aria-autocomplete="list"
          aria-controls={shown === undefined ? undefined : listId}
          aria-activedescendant={
            shown === undefined ? undefined : optionId(index)
          }
          onChange={(e) => {
            onSource(e.currentTarget.value);
            readCaret(e.currentTarget);
          }}
          onSelect={(e) => readCaret(e.currentTarget)}
          onFocus={(e) => {
            setFocused(true);
            readCaret(e.currentTarget);
          }}
          onBlur={() => setFocused(false)}
          onKeyDown={onKeyDown}
        />
      </div>
      <p role="status" className="sr-only">
        {shown === undefined ? "" : t("expr.complete.count", { count: total })}
      </p>
      {shown !== undefined && (
        // The offers are 8 px from the field, and from each other, as a
        // finger's targets are (PRD NFR-TOUCH-01). A press anywhere on the
        // list, on an offer or between two, leaves the focus in the field,
        // so that one that misses an offer closes neither the list nor the
        // on-screen keyboard.
        // biome-ignore lint/a11y/noStaticElementInteractions: a press on it does nothing but leave the focus in the field, whose keys work the list.
        <div
          className="space-y-1 rounded-md border border-border p-2"
          onMouseDown={(event) => event.preventDefault()}
        >
          <div
            id={listId}
            role="listbox"
            aria-label={t("expr.complete.label")}
            className="flex max-h-72 flex-col gap-2 overflow-y-auto"
          >
            {offers.map((offer, i) => (
              // The field's keys move through the offers and choose one
              // (aria-activedescendant), which keeps the focus in the
              // field, so an offer takes none itself.
              // biome-ignore lint/a11y/useFocusableInteractive: as above.
              // biome-ignore lint/a11y/useKeyWithClickEvents: as above.
              <div
                key={offer.id}
                id={optionId(i)}
                role="option"
                aria-selected={i === index}
                data-offer={offer.id}
                onClick={() => {
                  const el = area.current;
                  const now =
                    el === null
                      ? undefined
                      : spotAt(el.value, el.selectionStart);
                  if (now !== undefined) {
                    choose(offer, now);
                  }
                }}
                className="flex min-h-11 cursor-pointer flex-col justify-center rounded px-3 py-1 aria-selected:bg-surface-hover"
              >
                <span className="flex flex-wrap items-baseline gap-x-3">
                  <code className="font-mono break-all">{offer.label}</code>
                  <span className="text-fg-muted">{offer.detail}</span>
                </span>
                {offer.description !== undefined && (
                  <span className="text-sm break-words">
                    {offer.description}
                  </span>
                )}
                {i === index && offer.variable !== undefined && (
                  <span className="text-sm">
                    <Writes projectId={projectId} id={offer.variable} />
                  </span>
                )}
              </div>
            ))}
          </div>
          {total > offers.length && (
            <p className="px-3 text-fg-muted">
              {t("expr.complete.more", { count: total - offers.length })}
            </p>
          )}
        </div>
      )}
    </>
  );
}

/** The editor of an expression's source (the module's comment says what it holds). */
export function ExpressionEditor({
  projectId,
  boardId,
  context,
  label,
  hint,
  defaultValue,
  stored,
  error,
  optional,
  guided = false,
  onChange,
}: ExpressionEditorProps) {
  const { t, i18n } = useTranslation();
  const words = useMemo<Words>(
    () => ({ t, locale: i18n.language }),
    [t, i18n.language],
  );
  const scope = useScope(projectId, boardId);
  const id = useId();
  const labelId = `${id}-label`;
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const refusalId = `${id}-refusal`;
  const area = useRef<HTMLTextAreaElement>(null);
  const [source, setSource] = useState(defaultValue);
  const [view, setView] = useState<"text" | "guided">("text");
  const analysis = useMemo(
    () => analyze(source, context, scope),
    [source, context, scope],
  );
  const update = (next: string) => {
    setSource(next);
    onChange(next);
  };
  // The empty source of an optional expression is none, which says
  // nothing; any other's first error is the editor's, or, of the source
  // that the owner holds, the server's, where the editor finds none, as of
  // a node on a board that the page has not read.
  const none = optional && source === "";
  const serverError =
    stored !== undefined &&
    stored.source === source &&
    !stored.is_valid &&
    stored.error !== undefined
      ? (stored.error as SourceError)
      : undefined;
  const shownError: SourceError | undefined = none
    ? undefined
    : (analysis.error ?? serverError);
  const err =
    shownError === undefined
      ? undefined
      : unitRange(analysis.units, shownError);
  const describedBy =
    [
      hint === undefined ? undefined : hintId,
      shownError === undefined ? undefined : errorId,
      error === undefined ? undefined : refusalId,
    ]
      .filter((s) => s !== undefined)
      .join(" ") || undefined;
  const focusField = () => area.current?.focus();
  return (
    <div className="flex flex-col gap-2" data-expression={context}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        {view === "text" ? (
          <label id={labelId} htmlFor={id} className="font-medium">
            {label}
          </label>
        ) : (
          <span id={labelId} className="font-medium">
            {label}
          </span>
        )}
        {guided && (
          <fieldset className="flex gap-2">
            <legend className="sr-only">
              {t("expr.view.legend", { field: label })}
            </legend>
            <button
              type="button"
              aria-pressed={view === "text"}
              onClick={() => setView("text")}
              className={`inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center rounded-md border border-border bg-surface px-3 font-medium hover:bg-surface-hover ${pressedClass}`}
            >
              {t("expr.view.text")}
            </button>
            <button
              type="button"
              aria-pressed={view === "guided"}
              onClick={() => setView("guided")}
              className={`inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center rounded-md border border-border bg-surface px-3 font-medium hover:bg-surface-hover ${pressedClass}`}
            >
              {t("expr.view.guided")}
            </button>
          </fieldset>
        )}
      </div>
      {hint !== undefined && (
        <p id={hintId} className="text-sm text-fg-muted">
          {hint}
        </p>
      )}
      {view === "text" ? (
        <TextView
          id={id}
          projectId={projectId}
          analysis={analysis}
          scope={scope}
          words={words}
          describedBy={describedBy}
          err={err}
          onSource={update}
          area={area}
        />
      ) : (
        <GuidedPicker
          labelledBy={labelId}
          analysis={analysis}
          scope={scope}
          words={words}
          describedBy={describedBy}
          onSource={update}
        />
      )}
      {shownError !== undefined && (
        <p
          id={errorId}
          className="text-sm text-danger"
          data-expression-error=""
        >
          {errorText(words, source, shownError)}
        </p>
      )}
      {error !== undefined && (
        <p id={refusalId} className="text-sm text-danger">
          {error}
        </p>
      )}
      <UnknownName
        projectId={projectId}
        analysis={analysis}
        scope={scope}
        onCreated={focusField}
      />
      {analysis.ast !== undefined && (
        <>
          <PlainText analysis={analysis} scope={scope} words={words} />
          <Names
            projectId={projectId}
            analysis={analysis}
            scope={scope}
            words={words}
          />
        </>
      )}
    </div>
  );
}
