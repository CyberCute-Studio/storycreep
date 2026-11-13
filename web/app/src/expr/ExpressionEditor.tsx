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
import { useInfiniteQuery } from "@tanstack/react-query";
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
import {
  boardNameOf,
  nodeTitleOf,
  siteText,
  variableUsagesQuery,
} from "../content/usages";
import { pressedClass } from "../ui/Button";
import { OfferFrame, OfferList, OfferOption } from "../ui/Offers";
import {
  type Analysis,
  analyze,
  asTyped,
  type Run,
  runsOf,
  typedRange,
  unitRange,
} from "./analyze";
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
  /** The expression as its owner holds it, whose first error, as the server compiled it, the editor shows while it shows that source, where it finds none itself and could not check it whole. */
  stored?: Expression;
  /** What a refusal of the form's save says of the field. */
  error?: string;
  /** Whether the empty source is no expression, as a connection's condition and a script are, rather than one that does not compile, as a branch output's condition. */
  optional: boolean;
  /** The view of a condition that offers the guided picker, which its owner keeps (useExpressionView), or none, of one that offers none. */
  guided?: ExpressionView;
  /**
   * Called with the source as it changes, and with whether a field of the
   * guided picker holds what it cannot write yet, as a number that is not
   * one, which the form's Save is refused for, rather than save the source
   * as it was before, as the field says (GuidedPicker).
   */
  onChange: (source: string, unfinished: boolean) => void;
}

/** Which view of a condition its editor shows: its text, or its guided picker. */
export type View = "text" | "guided";

/**
 * The view of a condition's editor, which its owner keeps outside the form
 * that holds the editor, so that the form shown anew, as once a save ends
 * (useHeld), shows the view that it showed, whose controls are where they
 * were, as the focus that goes back to its place among them needs.
 */
export interface ExpressionView {
  readonly view: View;
  readonly onView: (view: View) => void;
}

/** Returns the view of a condition's editor, its text first, which its owner keeps (ExpressionView). */
export function useExpressionView(): ExpressionView {
  const [view, setView] = useState<View>("text");
  return { view, onView: setView };
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
  // A range that covers characters is underlined; one that covers none is
  // a bar of its own between two, or at the end.
  let markerShown = err === undefined || err.from < err.to;
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

/**
 * Submits form as Enter in a field of one line does (HTML's implicit
 * submission): by its default button, its first submit button, unless that
 * is disabled, as while a save is under way, so that a second Enter sends
 * no second save, or by itself where it has none.
 */
function submitImplicitly(form: HTMLFormElement | null): void {
  if (form === null) {
    return;
  }
  const button = [...form.elements].find(
    (e): e is HTMLButtonElement | HTMLInputElement =>
      (e instanceof HTMLButtonElement || e instanceof HTMLInputElement) &&
      e.type === "submit",
  );
  if (button === undefined) {
    form.requestSubmit();
  } else if (!button.matches(":disabled")) {
    form.requestSubmit(button);
  }
}

/** How many of the sites that write a variable the highlighted offer of it tells of. */
const writesShown = 3;

/** How long what the highlighted offer read of where a variable is written holds, rather than be read again as the arrows come back to it. */
const writesKept = 30_000;

/**
 * Where the variable of id is written, as the first sites that its usage
 * list of its writes has, which the highlighted offer tells of: of a read
 * of a page of one more than it tells of, which says whether there are
 * more, rather than of the list's hundred, kept for a while (writesKept),
 * and which says nothing of a refusal of its own, as the offer says that
 * where it is written could not be read. A site is named by its board and
 * its node, as its usage list names them (UsageItem).
 */
function Writes({ projectId, id }: { projectId: string; id: string }) {
  const { t } = useTranslation();
  const writes = useInfiniteQuery({
    ...variableUsagesQuery(projectId, id, "write", writesShown + 1),
    staleTime: writesKept,
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
  const first = writes.data.pages[0];
  const items = first?.items ?? [];
  const sites = items.slice(0, writesShown).map((usage) =>
    usage.board_id === undefined
      ? siteText(t, usage, undefined)
      : t("expr.offer.write_site", {
          board: boardNameOf(t, usage),
          node: nodeTitleOf(t, usage),
        }),
  );
  return (
    <span className="text-fg-muted">
      {sites.length === 0
        ? t("expr.offer.written_nowhere")
        : items.length <= writesShown && (first?.next_cursor ?? null) === null
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
 * the list of what it offers to complete at the caret, below it, as a name
 * is typed, while the caret stays in it, and the field has the focus,
 * rather than wherever the caret comes to a name, as the arrows move it
 * through a script, and not of a name typed whole, of which an offer is
 * what it is already. The text area keeps the focus, as a combobox's
 * pattern has it (OfferFrame): its arrows move through the list
 * (aria-activedescendant), Enter and Tab write what is highlighted, which
 * closes the list until a key types again, Escape closes the list until the
 * caret leaves what it was of, and a click or a tap on an offer writes it
 * without taking the focus, so that the on-screen keyboard stays. Enter
 * elsewhere in a condition, which is one line, with Shift too, saves its
 * form, as Enter in a field of one line does, but not while its Save is
 * disabled (submitImplicitly).
 */
function TextView({
  id,
  projectId,
  analysis,
  scope,
  words,
  describedBy,
  err,
  refused,
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
  /** Whether a refusal of the form's save names the field. */
  refused: boolean;
  onSource: (source: string) => void;
  area: RefObject<HTMLTextAreaElement | null>;
}) {
  const { t } = words;
  const listId = useId();
  const { source, context } = analysis;
  const [caret, setCaret] = useState<number>();
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState<string>();
  // The spot that is typed, of which alone the list opens, until the caret
  // leaves it.
  const [typing, setTyping] = useState<string>();
  const [moved, setMoved] = useState<{ at: string; offer: string }>();
  const spot = useMemo(
    () => (caret === undefined ? undefined : spotAt(source, caret)),
    [source, caret],
  );
  const open =
    focused &&
    spot !== undefined &&
    typing === spotKey(spot) &&
    dismissed !== spotKey(spot)
      ? spot
      : undefined;
  const { offers, total } = useMemo(
    () =>
      open === undefined
        ? { offers: [] as Offer[], total: 0 }
        : offersAt(open, context, scope.view, scope.boardId, words),
    [open, context, scope, words],
  );
  // A name typed whole, of which an offer is what it is already, has nothing
  // to complete, and its keys are the field's: Enter saves a condition, and
  // Tab moves on, rather than write another offer that the list has first,
  // as global.trust, of a board's trust typed whole.
  const typedName = open === undefined ? "" : source.slice(open.from, open.to);
  const whole =
    open !== undefined &&
    offers.some((offer) => editOf(source, open, offer).text === typedName);
  const shown = offers.length > 0 && !whole ? open : undefined;
  const at = shown === undefined ? "" : spotKey(shown);
  const index =
    shown === undefined
      ? -1
      : Math.max(
          0,
          moved?.at === at ? offers.findIndex((o) => o.id === moved.offer) : 0,
        );
  const optionId = (i: number) => `${listId}-${i}`;
  // Reads where the caret is, of what is typed there where typed says so.
  const readCaret = (el: HTMLTextAreaElement, typed = false) => {
    const next =
      el.selectionStart === el.selectionEnd ? el.selectionStart : undefined;
    setCaret(next);
    const nextSpot = next === undefined ? undefined : spotAt(el.value, next);
    const nextKey = nextSpot === undefined ? undefined : spotKey(nextSpot);
    // An Escape closes the list of what the caret was in alone.
    if (dismissed !== undefined && nextKey !== dismissed) {
      setDismissed(undefined);
    }
    if (typed) {
      setTyping(nextKey);
    } else if (typing !== undefined && nextKey !== typing) {
      setTyping(undefined);
    }
  };
  const choose = (offer: Offer, where: Spot) => {
    const el = area.current;
    if (el === null) {
      return;
    }
    insertInto(el, editOf(el.value, where, offer));
    readCaret(el);
    // What is written ends what was typed, and the list closes until a key
    // types again, as one that shortens the name to choose another; one
    // that is to go on, as a component's key and its dot with nothing after
    // it yet, offers what follows.
    const next = spotAt(el.value, el.selectionStart);
    setTyping(
      next !== undefined && next.query === "" && next.from === next.to
        ? spotKey(next)
        : undefined,
    );
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
    // Enter, with Shift too, as a field of one line takes it, breaks no line
    // of a condition, which is one.
    if (
      event.key === "Enter" &&
      context === "condition" &&
      !event.altKey &&
      !event.ctrlKey &&
      !event.metaKey
    ) {
      event.preventDefault();
      submitImplicitly(event.currentTarget.form);
    }
  };
  // What the colours show changes with the source and its error alone, not
  // with the caret, nor the offer highlighted.
  const errFrom = err?.from;
  const errTo = err?.to;
  const colours = useMemo(
    () =>
      tinted(
        analysis,
        errFrom === undefined || errTo === undefined
          ? undefined
          : { from: errFrom, to: errTo },
      ),
    [analysis, errFrom, errTo],
  );
  return (
    <>
      <div className="expr-field rounded-md border border-border bg-canvas">
        <pre aria-hidden="true">
          {colours}
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
          aria-invalid={err !== undefined || refused || undefined}
          // An error that the field finds of its own source, which the
          // server keeps as it is, rather than refuses: a refusal of
          // another field leaves the focus to that one (useFocusRefusal).
          data-own-error={err !== undefined && !refused ? "" : undefined}
          aria-describedby={describedBy}
          aria-autocomplete="list"
          aria-controls={shown === undefined ? undefined : listId}
          aria-activedescendant={
            shown === undefined ? undefined : optionId(index)
          }
          onChange={(e) => {
            onSource(e.currentTarget.value);
            readCaret(e.currentTarget, true);
          }}
          onSelect={(e) => readCaret(e.currentTarget)}
          onFocus={(e) => {
            setFocused(true);
            readCaret(e.currentTarget);
          }}
          onBlur={() => {
            setFocused(false);
            setTyping(undefined);
          }}
          onKeyDown={onKeyDown}
        />
      </div>
      <p role="status" className="sr-only">
        {shown === undefined ? "" : t("expr.complete.count", { count: total })}
      </p>
      {shown !== undefined && (
        // Below the field, as N73's list is (OfferFrame).
        <OfferFrame className="space-y-1 rounded-md border border-border p-2">
          <OfferList
            id={listId}
            label={t("expr.complete.label")}
            maxHeight="max-h-72"
          >
            {offers.map((offer, i) => (
              <OfferOption
                key={offer.id}
                id={optionId(i)}
                selected={i === index}
                onChoose={() => {
                  const el = area.current;
                  const now =
                    el === null
                      ? undefined
                      : spotAt(el.value, el.selectionStart);
                  if (now !== undefined) {
                    choose(offer, now);
                  }
                }}
                layout="flex-col justify-center py-1"
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
              </OfferOption>
            ))}
          </OfferList>
          {total > offers.length && (
            <p className="px-3 text-fg-muted">
              {t("expr.complete.more", { count: total - offers.length })}
            </p>
          )}
        </OfferFrame>
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
  guided,
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
  const picker = useRef<HTMLFieldSetElement>(null);
  const [source, setSource] = useState(() => asTyped(defaultValue));
  const view = guided?.view ?? "text";
  // What the owner's form is told last: the source, and whether the
  // picker's number holds what it cannot write yet (onChange), of the
  // source as the owner holds it until either changes.
  const told = useRef({ source: defaultValue, unfinished: false });
  const analysis = useMemo(
    () => analyze(source, context, scope),
    [source, context, scope],
  );
  const tell = (next: Partial<typeof told.current>) => {
    told.current = { ...told.current, ...next };
    onChange(told.current.source, told.current.unfinished);
  };
  // A source written anew, as typed or as the picker writes it, of which
  // no field of the picker holds what it cannot write then.
  const update = (next: string) => {
    setSource(next);
    tell({ source: next, unfinished: false });
  };
  const showView = (next: View) => {
    guided?.onView(next);
    // The picker's number, which goes with it, holds nothing then.
    if (next === "text" && told.current.unfinished) {
      tell({ unfinished: false });
    }
  };
  // The empty source of an optional expression is none, which says
  // nothing; any other's first error is the editor's, or, of the source
  // that the owner holds, the server's, where the editor could not tell:
  // where it checked the syntax alone, as before the page has read the
  // variables, or took a node of a board that the page has not read for one
  // that is no jumper; not where it checked the source whole and found it
  // valid, as once a variable that it names is created, which the server
  // compiles it anew of, but the form shows as it held it.
  const none = optional && source === "";
  const serverError =
    stored !== undefined &&
    asTyped(stored.source) === source &&
    !stored.is_valid &&
    stored.error !== undefined &&
    (!analysis.checked || analysis.unread)
      ? typedRange(stored.source, stored.error as SourceError)
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
  // The field has the focus again, as once a variable is created inline:
  // the text area, or the picker's group, which the button pressed in it
  // goes with, and whose fields follow it.
  const focusField = () => (area.current ?? picker.current)?.focus();
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
        {guided !== undefined && (
          <fieldset className="flex gap-2">
            <legend className="sr-only">
              {t("expr.view.legend", { field: label })}
            </legend>
            <button
              type="button"
              aria-pressed={view === "text"}
              onClick={() => showView("text")}
              className={`inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center rounded-md border border-border bg-surface px-3 font-medium hover:bg-surface-hover ${pressedClass}`}
            >
              {t("expr.view.text")}
            </button>
            <button
              type="button"
              aria-pressed={view === "guided"}
              onClick={() => showView("guided")}
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
          refused={error !== undefined}
          onSource={update}
          area={area}
        />
      ) : (
        <GuidedPicker
          group={picker}
          labelledBy={labelId}
          analysis={analysis}
          scope={scope}
          words={words}
          describedBy={describedBy}
          onSource={update}
          onUnfinished={() => tell({ unfinished: true })}
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
