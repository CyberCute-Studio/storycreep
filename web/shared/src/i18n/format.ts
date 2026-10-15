// Locale-aware formatting of numbers, dates, and relative times (PRD
// FR-I18N-02), with the browser's Intl. Each function takes the locale that
// resolveLocale returned.

/** The styles of a date or a time, as Intl.DateTimeFormat names them. */
export type DateStyle = "short" | "medium" | "long" | "full";

/** Returns `value` in the locale's number format, as {n, number} does. */
export function formatNumber(
  locale: string,
  value: number,
  options?: Intl.NumberFormatOptions,
): string {
  return new Intl.NumberFormat(locale, options).format(value);
}

/** Returns the date of `date` in the locale's format. */
export function formatDate(
  locale: string,
  date: Date | number,
  style: DateStyle = "medium",
): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: style }).format(date);
}

/** Returns the date and time of `date` in the locale's format. */
export function formatDateTime(
  locale: string,
  date: Date | number,
  dateStyle: DateStyle = "medium",
  timeStyle: DateStyle = "short",
): string {
  return new Intl.DateTimeFormat(locale, { dateStyle, timeStyle }).format(date);
}

// The units of a relative time, largest first, with their length in
// seconds. A month and a year are their average lengths, which is close
// enough for "3 months ago".
const UNITS: readonly [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365.2425 * 86400],
  ["month", (365.2425 / 12) * 86400],
  ["week", 7 * 86400],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
  ["second", 1],
];

/**
 * Returns how long before or after `now` `date` is, in the locale's words,
 * in the largest unit in which it is at least one, rounded: "2 minutes
 * ago", "in 3 days", "yesterday".
 */
export function formatRelativeTime(
  locale: string,
  date: Date | number,
  now: Date | number = Date.now(),
): string {
  const seconds = (Number(date) - Number(now)) / 1000;
  const [unit, length] = UNITS.find(
    ([, length]) => Math.abs(seconds) >= length,
  ) ?? ["second", 1];
  // A half is rounded away from zero, so that 90 seconds is two minutes
  // before now as after it; Math.round would round -1.5 to -1.
  const count = Math.sign(seconds) * Math.round(Math.abs(seconds) / length);
  return new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(
    count,
    unit,
  );
}
