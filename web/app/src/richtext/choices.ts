// What the { and @ of a rich text offer (PRD FR-TEXT-02, FR-TEXT-03, plan
// note N73): the values and the components that the page gives the editor,
// narrowed to those whose names have a word that starts as each word typed
// after the trigger does, as the command palette narrows its commands
// (ui/match.ts), and a value's format, the digits after the point that a
// number is shown with, typed after a colon, as {gold:2.

import { foldName, foldWords, wordsMatcher } from "../ui/match";
import { isDecimals } from "./schema";
import type { Suggestion } from "./suggest";

/** A value that { offers: a variable's, or a component's runtime attribute's (FR-COMP-03). */
export interface ValueChoice {
  /** The variable's ID, or the component's. */
  ref: string;
  /** The attribute definition's ID, of a component's attribute's value. */
  attribute?: string;
  /** Its name, as an expression names it, as gold or elara.trust, which its reference shows. */
  name: string;
  /** What else the list says of it, as that it is a global variable. */
  detail: string;
  /** Other names that what is typed finds it by, as the component's and the attribute's own, as Elara and Trust. */
  terms?: readonly string[];
}

/** A component that @ offers to mention. */
export interface MentionChoice {
  /** The component's ID. */
  id: string;
  /** Its name, as Elara, which its mention shows. */
  name: string;
  /** What else the list says of it, as its type's name. */
  detail: string;
  /** Other names that what is typed finds it by, as its key. */
  terms?: readonly string[];
}

/** What a page's rich text may refer to, which its { and @ offer. */
export interface RichTextChoices {
  values: readonly ValueChoice[];
  mentions: readonly MentionChoice[];
}

/** What the list of a suggestion offers: a value, with the format typed after its name, if one was, or a component. */
export type Offer =
  | { kind: "value"; choice: ValueChoice; decimals?: number }
  | { kind: "mention"; choice: MentionChoice };

/** The most offers that the list shows, of a query that more match, which the user narrows by typing on. */
export const MAX_OFFERS = 20;

/**
 * Returns what was typed after { as the name that it narrows the values to
 * and the format of the one chosen: the digits after a last colon, from 0
 * to MAX_DECIMALS, as 2 of gold:2, or none, of a colon not yet followed by
 * them, as of gold:; a colon followed by anything else is part of the name,
 * which no value's has, so that it narrows the values to none.
 */
export function valueQuery(query: string): {
  name: string;
  decimals?: number;
} {
  const format = /:(\d?)$/.exec(query);
  if (format === null) {
    return { name: query };
  }
  const name = query.slice(0, format.index);
  if (format[1] === "") {
    return { name };
  }
  const decimals = Number(format[1]);
  return isDecimals(decimals) ? { name, decimals } : { name: query };
}

/** What the list compares of a choice: its name, folded whole, and the words of its name and of each of its terms, each folded as the palette folds them (ui/match.ts). */
interface Folded {
  name: string;
  words: readonly (readonly string[])[];
}

/**
 * The folds of each choice, made as it is first compared, and kept while
 * the choice is, rather than made again at each key typed, which would
 * take tens of milliseconds of a project of thousands of values; a page
 * gives the same choices until what they are made of changes
 * (useTextChoices).
 */
const folds = new WeakMap<object, Folded>();

/** Returns what the list compares of choice, as folds keeps it. */
function foldedOf(choice: { name: string; terms?: readonly string[] }): Folded {
  let folded = folds.get(choice);
  if (folded === undefined) {
    folded = {
      name: foldName(choice.name),
      words: [choice.name, ...(choice.terms ?? [])].map(foldWords),
    };
    folds.set(choice, folded);
  }
  return folded;
}

/**
 * Returns the choices whose name, or one of their terms, matches name, as
 * the palette's words do (wordsMatcher), those whose name starts as name
 * does first, whatever their case and diacritics, as the words are
 * matched, each in the order of choices.
 */
function matching<T extends { name: string; terms?: readonly string[] }>(
  choices: readonly T[],
  name: string,
): T[] {
  const matches = wordsMatcher(name);
  const start = foldName(name.trim());
  const found = choices.filter((c) => foldedOf(c).words.some(matches));
  if (start === "") {
    return found;
  }
  const starts = (c: T) => foldedOf(c).name.startsWith(start);
  const first = found.filter(starts);
  return first.length === found.length
    ? found
    : [...first, ...found.filter((c) => !starts(c))];
}

/**
 * Returns what the list of open offers of choices, at most MAX_OFFERS of
 * them, and how many match in all: the values that what was typed after {
 * names, with its format, or the components that what was typed after @
 * names.
 */
export function offersOf(
  choices: RichTextChoices,
  open: Pick<Suggestion, "trigger" | "query">,
): { offers: Offer[]; total: number } {
  if (open.trigger === "@") {
    const found = matching(choices.mentions, open.query);
    return {
      offers: found
        .slice(0, MAX_OFFERS)
        .map((choice) => ({ kind: "mention", choice })),
      total: found.length,
    };
  }
  const { name, decimals } = valueQuery(open.query);
  const found = matching(choices.values, name);
  return {
    offers: found
      .slice(0, MAX_OFFERS)
      .map((choice) => ({ kind: "value", choice, decimals })),
    total: found.length,
  };
}
