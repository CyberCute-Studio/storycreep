// The keys that order a folder's children among themselves, as the server's
// package orderkey checks them (PRD FR-PROJ-02): a string of base-62
// digits, 0-9, A-Z, and a-z, in that order, which is the order of their
// bytes, of at most maxKeyLength digits, that does not end in 0, read as the
// digits of a fraction between 0 and 1. Moving a child picks a key between
// the keys of the children that it lands between (keyBetween), so that the
// move changes that child alone. Children with the same key, as two clients
// may pick for two children dropped at one place, are ordered by their IDs
// without the prefix (compareChildren).

/** The digits of a key, in order. */
const digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";

/** The most digits that a key may have, as the server's orderkey.MaxLength. */
export const maxKeyLength = 128;

/** The pattern of a key, as the server's orderkey.Pattern. */
const keyPattern = /^[0-9A-Za-z]*[1-9A-Za-z]$/;

/** Reports whether key is a key that the server takes. */
export function isKey(key: string): boolean {
  return key.length <= maxKeyLength && keyPattern.test(key);
}

/**
 * Returns a key between before and after, either of which may be undefined
 * for none, as for a child that goes first or last, or undefined if there is
 * none of at most maxKeyLength digits, as between two keys that are the same,
 * or that are next to each other at the most digits that a key may have:
 * the server then places the child after the last, which a move without a
 * key asks for. Neither before nor after may be other than a key, and before
 * must come before after.
 */
export function keyBetween(
  before: string | undefined,
  after: string | undefined,
): string | undefined {
  if (
    (before !== undefined && !isKey(before)) ||
    (after !== undefined && !isKey(after)) ||
    (before !== undefined && after !== undefined && before >= after)
  ) {
    return undefined;
  }
  const key = midpoint(before ?? "", after);
  return isKey(key) ? key : undefined;
}

/**
 * Returns the digits of a fraction between 0.low and 0.high, or 1 if high is
 * undefined, where low < high, neither ends in 0, and low may be "" for 0:
 * the digits that the two share, then a digit halfway between their next
 * ones, or, where those are next to each other, the digits after low's that
 * go between them, as 7V between 7 and 8. The result never ends in 0.
 */
function midpoint(low: string, high: string | undefined): string {
  if (high !== undefined) {
    // The digits that the two share stay, low read with as many 0s after
    // it as high has digits: high cannot end within what they share, since
    // it is the larger, and ends in another digit than 0.
    let shared = 0;
    while ((low[shared] ?? "0") === high[shared]) {
      shared++;
    }
    if (shared > 0) {
      return (
        high.slice(0, shared) + midpoint(low.slice(shared), high.slice(shared))
      );
    }
  }
  const lowDigit = low === "" ? 0 : digits.indexOf(low[0]);
  const highDigit =
    high === undefined ? digits.length : digits.indexOf(high[0]);
  if (highDigit - lowDigit > 1) {
    return digits[Math.round((lowDigit + highDigit) / 2)];
  }
  // The first digits are next to each other. A high with more digits has
  // its first alone between the two, as 8 between 79 and 8A.
  if (high !== undefined && high.length > 1) {
    return high[0];
  }
  // Otherwise low's first digit stays, and the key goes on between the rest
  // of low and the end, as 7V between 7 and 8, or 7zV between 7z and 8.
  return digits[lowDigit] + midpoint(low.slice(1), undefined);
}

/**
 * Returns the key that places a child before the one at at among siblings,
 * the other children of their parent, in order, or undefined if there is
 * none (keyBetween): one before those before it that share its key too, as
 * no key goes between two that are the same.
 */
export function keyBeforeSibling(
  siblings: readonly { order: string }[],
  at: number,
): string | undefined {
  const target = siblings[at];
  const earlier = siblings.slice(0, at).findLast((c) => c.order < target.order);
  return keyBetween(earlier?.order, target.order);
}

/**
 * Returns the key that places a child after the one at at among siblings,
 * the other children of their parent, in order, or undefined if there is
 * none (keyBetween): one after those after it that share its key too.
 */
export function keyAfterSibling(
  siblings: readonly { order: string }[],
  at: number,
): string | undefined {
  const target = siblings[at];
  const later = siblings.slice(at + 1).find((c) => c.order > target.order);
  return keyBetween(target.order, later?.order);
}

/** What compareChildren reads of a child: its key and its ID. */
export interface Ordered {
  /** The child's ID, as bd_… or fld_…. */
  id: string;
  /** The child's key among its siblings. */
  order: string;
}

/**
 * Compares two children of a folder in the order that the server lists
 * them: by their keys, as their bytes compare, and two with the same key by
 * their IDs without the prefix, bd_ or fld_, whose characters order their
 * UUIDs, so that a board and a folder with the same key are ordered as the
 * server orders them.
 */
export function compareChildren(a: Ordered, b: Ordered): number {
  return (
    compareBytes(a.order, b.order) ||
    compareBytes(unprefixed(a.id), unprefixed(b.id))
  );
}

/** Returns id without its prefix and its underscore. */
function unprefixed(id: string): string {
  return id.slice(id.indexOf("_") + 1);
}

/**
 * Compares two strings of ASCII characters as their bytes compare, which
 * the code units of JavaScript's strings compare alike.
 */
function compareBytes(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}
