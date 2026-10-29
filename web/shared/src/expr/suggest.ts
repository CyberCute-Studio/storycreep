// The near matches that an unknown name or function suggests (§5.8.3).

/** The most near matches that an error suggests. */
export const MAX_SUGGESTIONS = 3;

// lowerASCII returns s with its ASCII letters in lower case, as the Go
// package lowers a name, which is of ASCII alone.
function lowerASCII(s: string): string {
  return s.replace(/[A-Z]/g, (c) => String.fromCharCode(c.charCodeAt(0) + 32));
}

/**
 * Returns the candidates that are near name, nearest first, at most
 * MAX_SUGGESTIONS of them, as an error suggests them, so that every
 * implementation suggests the same: those whose distance from name, with
 * their ASCII letters in lower case, is at most a third of name's length,
 * and at least 1, but less than name's length, so that a name of one
 * character is near only itself in another case; ordered by their distance,
 * and then by their code units, which of ASCII are its bytes, each once.
 * The distance is the optimal string alignment distance: the fewest
 * insertions, deletions, and substitutions of a character, and swaps of two
 * adjacent ones, that make one the other, no character changed twice.
 */
export function near(name: string, candidates: Iterable<string>): string[] {
  const limit = Math.min(
    Math.max(1, Math.floor(name.length / 3)),
    name.length - 1,
  );
  const matches: [string, number][] = [];
  const lower = lowerASCII(name);
  const rows: Rows = [[], [], []];
  for (const c of candidates) {
    // The distance is at least the difference of the lengths.
    if (Math.abs(c.length - name.length) > limit) {
      continue;
    }
    const d = distance(lower, lowerASCII(c), limit, rows);
    if (d <= limit) {
      matches.push([c, d]);
    }
  }
  matches.sort(([a, da], [b, db]) => da - db || (a < b ? -1 : a > b ? 1 : 0));
  const out: string[] = [];
  for (const [s] of matches) {
    if (out.length === MAX_SUGGESTIONS) {
      break;
    }
    if (out[out.length - 1] !== s) {
      out.push(s);
    }
  }
  return out;
}

// Rows are three rows of the distances of a's prefixes from b's: the one
// before the previous one, the previous one, and the current one.
type Rows = [number[], number[], number[]];

// distance returns the optimal string alignment distance of a and b, or a
// distance above limit once it is certain to be more than limit. It keeps
// the distances in rows, which it grows to b's length, so that one near
// reuses them for each of its candidates.
function distance(a: string, b: string, limit: number, rows: Rows): number {
  for (const row of rows) {
    while (row.length <= b.length) {
      row.push(0);
    }
  }
  let [prev2, prev, cur] = rows;
  for (let j = 0; j <= b.length; j++) {
    prev[j] = j;
  }
  for (let i = 1; i <= a.length; i++) {
    cur[0] = i;
    let least = cur[0];
    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      let d = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost);
      if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) {
        d = Math.min(d, prev2[j - 2] + 1);
      }
      cur[j] = d;
      least = Math.min(least, d);
    }
    if (least > limit) {
      return limit + 1;
    }
    const done = prev2;
    prev2 = prev;
    prev = cur;
    cur = done;
  }
  return prev[b.length];
}
