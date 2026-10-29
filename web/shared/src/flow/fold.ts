// The case folding by which the loader compares a node's tags, which are the
// same but for their case when they fold alike (schema/published/README.md):
// the Go runtime's, input.FoldCase, of golang.org/x/text, by the tables of
// schema/unicode/fold.json, which server/internal/input generates from Go's
// own folding and normalization form C, rather than by JavaScript's
// toLowerCase and normalize, which follow other rules, and the platform's
// version of Unicode, which may be older than Go's, as on a tablet whose
// system has not been updated, or newer.

import foldTable from "../../../../schema/unicode/fold.json" with {
  type: "json",
};

// MAX_NON_STARTERS is how many non-starters in a row the Go runtime's form C
// takes: before a code point that would make more, it inserts CGJ, U+034F
// COMBINING GRAPHEME JOINER, as Unicode's Stream-Safe Text Format does (UAX
// #15), and the platform's form C does not.
const MAX_NON_STARTERS = 30;
const CGJ = String.fromCodePoint(0x34f);

// The Hangul syllables and their jamo, which Unicode decomposes and composes
// by an algorithm (§3.12) rather than by the tables: a syllable is a leading
// consonant (L), a vowel (V), and, but for the first of every T_COUNT
// syllables, a trailing consonant (T).
const S_BASE = 0xac00;
const L_BASE = 0x1100;
const V_BASE = 0x1161;
const T_BASE = 0x11a7;
const L_COUNT = 19;
const V_COUNT = 21;
const T_COUNT = 28;
const N_COUNT = V_COUNT * T_COUNT;
const S_COUNT = L_COUNT * N_COUNT;

// Tables are what foldCase reads of fold.json: the code points that the
// folding changes, and what it folds each to; the ranges of the code points
// that form C may change, reorder, or join with the code points beside them
// (composing), each as its first and its last, in their order, which it
// leaves every other code point as it is beside, and joins it with none, as
// it does each that its version of Unicode does not assign; the ranges of
// the code points that form C counts as non-starters, at their start or at
// their end, each as its first and its last code point, then how many it
// counts each of them as at its start and at its end, in their order, which
// counts every other code point as none, and each of which is composing; the
// canonical combining class of each code point whose class is not 0; the
// canonical decomposition of each code point that form D changes but the
// Hangul syllables; and the code point that form C joins each pair of two
// into but the Hangul syllables, by pairKey.
interface Tables {
  folds: ReadonlyMap<number, string>;
  composing: readonly (readonly number[])[];
  nonStarters: readonly (readonly number[])[];
  combiningClasses: ReadonlyMap<number, number>;
  decompositions: ReadonlyMap<number, readonly number[]>;
  composites: ReadonlyMap<number, number>;
}

let built: Tables | undefined;

// tables returns the Tables, which it builds of fold.json the first time it
// is called, so that a page builds them only once it folds a tag.
function tables(): Tables {
  built ??= {
    folds: new Map(
      foldTable.fold.map(([c, ...to]) => [c, String.fromCodePoint(...to)]),
    ),
    composing: foldTable.nfc,
    nonStarters: foldTable.nonStarters,
    combiningClasses: new Map(
      foldTable.ccc.flatMap(([first, last, ccc]) =>
        Array.from({ length: last - first + 1 }, (_, k): [number, number] => [
          first + k,
          ccc,
        ]),
      ),
    ),
    decompositions: new Map(
      foldTable.decompositions.map(([c, ...to]) => [c, to]),
    ),
    composites: new Map(
      foldTable.compositions.map(([c, first, second]) => [
        pairKey(first, second),
        c,
      ]),
    ),
  };
  return built;
}

// pairKey returns the key of the pair of the code points first and second
// in the composites of the Tables.
function pairKey(first: number, second: number): number {
  return first * 0x110000 + second;
}

// rangeOf returns the range of ranges, each of which is its first and its
// last code point, and what else it holds, in their order, that holds the
// code point c, or undefined of none.
function rangeOf(
  ranges: readonly (readonly number[])[],
  c: number,
): readonly number[] | undefined {
  let lo = 0;
  let hi = ranges.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (ranges[mid][1] < c) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return lo < ranges.length && ranges[lo][0] <= c ? ranges[lo] : undefined;
}

// compose returns the code point that form C joins first and second into,
// by the algorithm of the Hangul syllables or by the Tables' composites, or
// undefined when it does not join them.
function compose(t: Tables, first: number, second: number): number | undefined {
  if (
    first >= L_BASE &&
    first < L_BASE + L_COUNT &&
    second >= V_BASE &&
    second < V_BASE + V_COUNT
  ) {
    return S_BASE + ((first - L_BASE) * V_COUNT + second - V_BASE) * T_COUNT;
  }
  if (
    first >= S_BASE &&
    first < S_BASE + S_COUNT &&
    (first - S_BASE) % T_COUNT === 0 &&
    second > T_BASE &&
    second < T_BASE + T_COUNT
  ) {
    return first + second - T_BASE;
  }
  return t.composites.get(pairKey(first, second));
}

/**
 * Returns s in Unicode's normalization form C as the Go runtime's puts a run
 * of code points that it inserts no CGJ in, by the tables of
 * schema/unicode/fold.json, whose version of Unicode is Go's, rather than by
 * the platform's form C (UAX #15): each code point decomposed, the
 * non-starters of each row of them then in the order of their combining
 * classes, kept where two are of one class, and each code point then joined,
 * from the first, with the last starter before it, as long as nothing
 * between the two that is left is a starter or of its class or a greater
 * one, which a starter joins only with the starter just before it.
 */
export function nfc(s: string): string {
  const t = tables();
  const d: number[] = [];
  for (const c of s) {
    const cp = c.codePointAt(0) as number;
    const h = cp - S_BASE;
    if (h >= 0 && h < S_COUNT) {
      d.push(
        L_BASE + Math.floor(h / N_COUNT),
        V_BASE + Math.floor((h % N_COUNT) / T_COUNT),
      );
      if (h % T_COUNT !== 0) {
        d.push(T_BASE + (h % T_COUNT));
      }
    } else {
      const to = t.decompositions.get(cp);
      if (to === undefined) {
        d.push(cp);
      } else {
        d.push(...to);
      }
    }
  }
  const ccc = (cp: number) => t.combiningClasses.get(cp) ?? 0;
  for (let i = 1; i < d.length; i++) {
    const c = ccc(d[i]);
    for (let j = i; c !== 0 && j > 0 && ccc(d[j - 1]) > c; j--) {
      [d[j - 1], d[j]] = [d[j], d[j - 1]];
    }
  }
  const out: number[] = [];
  // starter is where in out the last starter is, and last the class of the
  // last code point that out holds after it, 0 when it holds none, and 256
  // when out starts with no starter, so that nothing joins with its first.
  let starter = 0;
  let last = 0;
  for (const cp of d) {
    const c = ccc(cp);
    if (out.length === 0) {
      last = c === 0 ? 0 : 256;
      out.push(cp);
      continue;
    }
    const joined = compose(t, out[starter], cp);
    if (joined !== undefined && (last === 0 || last < c)) {
      out[starter] = joined;
      continue;
    }
    if (c === 0) {
      starter = out.length;
    }
    last = c;
    out.push(cp);
  }
  let r = "";
  for (const cp of out) {
    r += String.fromCodePoint(cp);
  }
  return r;
}

// formC returns s in Unicode's normalization form C as the Go runtime puts
// it in it. It inserts a CGJ before each code point that would make more
// than MAX_NON_STARTERS non-starters in a row, counted as the Tables'
// nonStarters count them, where a starter, or a code point that is not
// composing, ends a row, as Go's does. It puts each run of composing code
// points between them in form C by the Tables (nfc), and leaves each other
// code point as it is, as Go leaves it, though a platform's form C of a later
// version of Unicode may join one that Unicode assigned since with its
// neighbours, as Unicode 16.0's joins U+16D67 KIRAT RAI VOWEL SIGN E twice
// into U+16D68.
function formC(s: string): string {
  const t = tables();
  let out = "";
  let run = "";
  const flush = () => {
    if (run !== "") {
      out += nfc(run);
      run = "";
    }
  };
  // How many non-starters in a row run ends with.
  let row = 0;
  for (const c of s) {
    const cp = c.codePointAt(0) as number;
    if (!rangeOf(t.composing, cp)) {
      flush();
      out += c;
      row = 0;
      continue;
    }
    const counts = rangeOf(t.nonStarters, cp);
    const lead = counts === undefined ? 0 : counts[2];
    if (lead === 0) {
      row = counts === undefined ? 0 : counts[3];
    } else if (row + lead > MAX_NON_STARTERS) {
      flush();
      out += CGJ;
      row = lead;
    } else {
      row += lead;
    }
    run += c;
  }
  flush();
  return out;
}

/**
 * Returns the key by which two tags of a node are the same but for their
 * case, as the Go runtime's, input.FoldCase, is: the tag in Unicode's
 * normalization form C, each of its code points case folded as
 * schema/unicode/fold.json folds it, which is the Go runtime's folding, of
 * golang.org/x/text, rather than JavaScript's toUpperCase and toLowerCase,
 * which follow other rules and other versions of Unicode, and the result in
 * form C again; so that Quest, QUEST, and quest are one, and so are ẞ, ß,
 * and ss, though kır and kir are two, as ı folds to itself. Its forms C are
 * Go's too (formC), by the table's decompositions, combining classes, and
 * compositions, which are of Go's version of Unicode, rather than by the
 * platform's form C, which is of the platform's, so that a code point that
 * Unicode assigned after Go's version is left as it is, as Go leaves it, and
 * a mark of Go's version is ordered and joined as Go orders and joins it on
 * a platform of an older version, which takes it for an unassigned starter;
 * with a CGJ inserted after 30 non-starters in a row, where the table says
 * that Go's inserts one, and the platform's inserts none.
 */
export function foldCase(tag: string): string {
  const { folds } = tables();
  let folded = "";
  for (const c of formC(tag)) {
    folded += folds.get(c.codePointAt(0) as number) ?? c;
  }
  return formC(folded);
}
