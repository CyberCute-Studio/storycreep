// How what the user types narrows a list of names, as the command palette's
// commands (PRD FR-EDIT-10) and the values and components that the rich
// text's { and @ offer (FR-TEXT-02, FR-TEXT-03): to the names that have a
// word that starts as each word typed does, whatever their case and
// diacritics.

import { foldCase } from "@storycreep/shared/fold";

/** Returns text folded as the palette compares it: by case, as the server folds tags, and without diacritics. */
export function foldName(text: string): string {
  return foldCase(text.normalize("NFD").replace(/\p{M}/gu, ""));
}

/** Returns the words of text, folded as foldName folds it. */
export function foldWords(text: string): string[] {
  return foldName(text)
    .split(/[^\p{L}\p{N}]+/u)
    .filter((w) => w !== "");
}

/**
 * Returns what reports whether the words of a name, as foldWords gives
 * them, have a word that starts as each word of query does, which query is
 * folded for once, rather than for each name that the palette lists: every
 * name, of a query of nothing but spaces, as of none; and none, of one that
 * has no word, as + or ?, which no name's words start as, so that the
 * palette lists the search for it alone, which Enter runs, rather than
 * every command, as if nothing had been typed.
 */
export function wordsMatcher(
  query: string,
): (words: readonly string[]) => boolean {
  const asked = foldWords(query);
  if (asked.length === 0) {
    const all = query.trim() === "";
    return () => all;
  }
  return (words) => asked.every((q) => words.some((w) => w.startsWith(q)));
}

/** Returns what reports whether a name has a word that starts as each word of query does, as wordsMatcher has it, of the name's words. */
export function nameMatcher(query: string): (name: string) => boolean {
  const matches = wordsMatcher(query);
  return (name) => matches(foldWords(name));
}
