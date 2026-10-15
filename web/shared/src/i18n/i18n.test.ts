import { parse } from "@formatjs/icu-messageformat-parser";
import { afterEach, describe, expect, it, vi } from "vitest";
import rawVectors from "../../../../schema/i18n/vectors.json" with {
  type: "json",
};
import {
  createI18n,
  formatDate,
  formatDateTime,
  formatNumber,
  formatRelativeTime,
  matchLocale,
  resolveLocale,
} from "./index";

// The no-break space, which Czech groups digits with.
const NBSP = String.fromCharCode(0xa0);

// Catalogs for the tests: the shared catalogs have no messages yet.
const testCatalogs = {
  en: {
    "files.count": "{count, plural, one {# file} other {# files}}",
    "board.count": "{count, plural, one {# board} other {# boards}}",
    greeting: "Hello, {name}!",
  },
  cs: {
    "files.count":
      "{count, plural, one {# soubor} few {# soubory} many {# souboru} other {# souborů}}",
    greeting: "Ahoj, {name}!",
  },
};

// schema/i18n/vectors.json, shared with the server. See
// schema/i18n/README.md.
interface Vectors {
  format: {
    note: string;
    locale: string;
    message: string;
    args: Record<string, string | number>;
    output: string;
  }[];
  invalid: { note: string; message: string }[];
}

const vectors = rawVectors as Vectors;

afterEach(() => {
  vi.restoreAllMocks();
});

// Verifies: FR-I18N-01/AC2
it("renders the shared vectors as the server does, in English and Czech", () => {
  expect(vectors.format.length).toBeGreaterThan(0);
  for (const v of vectors.format) {
    const i18n = createI18n(v.locale, {
      catalogs: { en: { m: v.message }, [v.locale]: { m: v.message } },
    });
    expect(i18n.resolvedLanguage, v.note).toBe(v.locale);
    expect(i18n.t("m", v.args), v.note).toBe(v.output);
  }
});

// JSON cannot hold NaN and the infinities, so they are outside the vectors.
// The server formats them as these do (TestArgumentTypes in
// server/internal/i18n).
it("formats NaN and the infinities as the server does", () => {
  const cases: [string, number, string][] = [
    ["{n, number}", Number.NaN, "NaN"],
    ["{n, number, integer}", -Infinity, "-∞"],
    ["{n, plural, one {# one} other {# other}}", Infinity, "∞ other"],
    [
      "{n, plural, offset:1 one {# one} other {# other}}",
      -Infinity,
      "-∞ other",
    ],
    ["{n, selectordinal, one {#st} other {#th}}", Number.NaN, "NaNth"],
  ];
  for (const [message, n, output] of cases) {
    const i18n = createI18n("en", { catalogs: { en: { m: message } } });
    expect(i18n.t("m", { n }), message).toBe(output);
  }
});

it("refuses the shared vectors' invalid messages", () => {
  expect(vectors.invalid.length).toBeGreaterThan(0);
  for (const v of vectors.invalid) {
    expect(
      () => parse(v.message, { ignoreTag: true, requiresOtherClause: true }),
      v.note,
    ).toThrow();
  }
});

// Verifies: FR-I18N-01/AC2
it("formats a catalog's plurals and arguments with English and Czech plural rules", () => {
  const en = createI18n("en", { catalogs: testCatalogs });
  const cs = createI18n("cs-CZ", { catalogs: testCatalogs });
  expect(en.t("files.count", { count: 1 })).toBe("1 file");
  expect(en.t("files.count", { count: 2 })).toBe("2 files");
  expect(en.t("files.count", { count: 1200 })).toBe("1,200 files");
  expect(cs.t("files.count", { count: 1 })).toBe("1 soubor");
  expect(cs.t("files.count", { count: 3 })).toBe("3 soubory");
  expect(cs.t("files.count", { count: 5 })).toBe("5 souborů");
  expect(cs.t("files.count", { count: 1.5 })).toBe("1,5 souboru");
  expect(cs.t("files.count", { count: 1200 })).toBe(`1${NBSP}200 souborů`);
  expect(cs.t("greeting", { name: "Ada" })).toBe("Ahoj, Ada!");
  // A message that the Czech catalog lacks is in English, formatted with
  // English plural rules and numbers, as the server formats it.
  expect(cs.t("board.count", { count: 3 })).toBe("3 boards");
  expect(cs.t("board.count", { count: 1200 })).toBe("1,200 boards");
  expect(cs.t("board.count", { count: 1.5 })).toBe("1.5 boards");
  // And a message that it has is still in Czech after one that it lacks.
  expect(cs.t("files.count", { count: 1200 })).toBe(`1${NBSP}200 souborů`);
});

// Verifies: FR-I18N-02/AC2
it("falls back to English for an unknown locale without errors", () => {
  const warn = vi.spyOn(console, "warn");
  const error = vi.spyOn(console, "error");
  for (const locale of ["fr", "xx-YY", "not a tag!", "", "*"]) {
    const i18n = createI18n(locale, { catalogs: testCatalogs });
    expect(i18n.resolvedLanguage, locale).toBe("en");
    expect(i18n.t("greeting", { name: "Ada" }), locale).toBe("Hello, Ada!");
    expect(
      resolveLocale({ preference: locale, accepted: [locale] }, ["en", "cs"]),
    ).toBe("en");
  }
  expect(warn).not.toHaveBeenCalled();
  expect(error).not.toHaveBeenCalled();
});

it("shows a missing key or a message without its arguments as it is", () => {
  const en = createI18n("en", { catalogs: testCatalogs });
  expect(en.t("no.such.key")).toBe("no.such.key");
  expect(en.t("greeting")).toBe("Hello, {name}!");
});

it("treats a message left empty in a translation as missing", () => {
  const cs = createI18n("cs", {
    catalogs: { en: { saved: "Saved." }, cs: { saved: "" } },
  });
  expect(cs.t("saved")).toBe("Saved.");
});

it("switches the locale without a new instance", async () => {
  const i18n = createI18n("en", { catalogs: testCatalogs });
  await i18n.changeLanguage("cs");
  expect(i18n.t("greeting", { name: "Ada" })).toBe("Ahoj, Ada!");
});

describe("resolveLocale", () => {
  const supported = ["en", "cs"];
  it("takes the user's preference first", () => {
    expect(
      resolveLocale(
        { preference: "cs", accepted: ["en"], instanceDefault: "en" },
        supported,
      ),
    ).toBe("cs");
  });
  it("takes the first of the browser's locales that has a catalog", () => {
    expect(
      resolveLocale(
        { preference: "de", accepted: ["fr", "cs-CZ", "en"] },
        supported,
      ),
    ).toBe("cs");
  });
  it("takes the instance's default last", () => {
    expect(
      resolveLocale(
        { preference: null, accepted: ["fr"], instanceDefault: "cs" },
        supported,
      ),
    ).toBe("cs");
    expect(resolveLocale({}, supported)).toBe("en");
  });
});

describe("matchLocale", () => {
  it("matches the same language in the same script", () => {
    expect(matchLocale("en-GB", ["en", "cs"])).toBe("en");
    expect(matchLocale("CS", ["en", "cs"])).toBe("cs");
    expect(matchLocale("zh-Hant", ["en", "zh"])).toBeUndefined();
    expect(matchLocale("sr-Latn", ["sr"])).toBeUndefined();
    expect(matchLocale("sk", ["en", "cs"])).toBeUndefined();
    expect(matchLocale("en_US", ["en"])).toBeUndefined();
  });
  // As the server's Bundle.Match does (TestMatchRegion).
  it("matches the catalog of the same region first", () => {
    const regional = ["en", "en-GB", "pt-BR", "pt-PT"];
    expect(matchLocale("en-GB", regional)).toBe("en-GB");
    expect(matchLocale("en-US", regional)).toBe("en");
    expect(matchLocale("en", regional)).toBe("en");
    expect(matchLocale("pt-PT", regional)).toBe("pt-PT");
    expect(matchLocale("pt-BR", regional)).toBe("pt-BR");
    expect(matchLocale("pt", regional)).toBe("pt-BR");
  });
});

describe("formatting", () => {
  // In local time, which Intl formats in, whatever the time zone.
  const date = new Date(2026, 9, 15, 12, 30).getTime();
  it("formats numbers", () => {
    expect(formatNumber("en", 1234.5)).toBe("1,234.5");
    expect(formatNumber("cs", 1234.5)).toBe(`1${NBSP}234,5`);
    expect(formatNumber("en", 0.25, { style: "percent" })).toBe("25%");
  });
  it("formats dates and times", () => {
    expect(formatDate("en", date, "long")).toBe("October 15, 2026");
    expect(formatDate("cs", date, "long")).toBe("15. října 2026");
    // ICU separates the time from AM or PM with a narrow no-break space.
    expect(formatDateTime("en", date, "short", "short")).toMatch(
      /^10\/15\/26, 12:30\sPM$/,
    );
    expect(formatDateTime("cs", date)).toBe("15. 10. 2026 12:30");
  });
  it("formats relative times", () => {
    expect(formatRelativeTime("en", date - 90_000, date)).toBe("2 minutes ago");
    expect(formatRelativeTime("cs", date - 90_000, date)).toBe(
      "před 2 minutami",
    );
    expect(formatRelativeTime("en", date - 86_400_000, date)).toBe("yesterday");
    expect(formatRelativeTime("en", date + 3 * 86_400_000, date)).toBe(
      "in 3 days",
    );
    expect(formatRelativeTime("en", date, date)).toBe("now");
    // A count that rounds up to the next unit is in that unit.
    expect(formatRelativeTime("en", date + 3_576_000, date)).toBe("in 1 hour");
    expect(formatRelativeTime("en", date - 59_600, date)).toBe("1 minute ago");
    expect(formatRelativeTime("en", date + 6.6 * 86_400_000, date)).toBe(
      "next week",
    );
    expect(formatRelativeTime("en", date - 364 * 86_400_000, date)).toBe(
      "last year",
    );
  });
});
