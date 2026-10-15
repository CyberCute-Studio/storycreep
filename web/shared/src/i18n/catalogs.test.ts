import { readdirSync } from "node:fs";
import {
  isPluralElement,
  isSelectElement,
  isTagElement,
  type MessageFormatElement,
  parse,
} from "@formatjs/icu-messageformat-parser";
import { expect, it } from "vitest";
import { type Catalog, catalogs, SOURCE_LOCALE } from "./index";

// The arguments that a message's elements use: the names of its arguments,
// in any option of a plural or select.
function argumentsOf(elements: MessageFormatElement[], into: Set<string>) {
  for (const el of elements) {
    if ("value" in el && typeof el.value === "string" && el.type !== 0) {
      into.add(el.value);
    }
    if (isPluralElement(el) || isSelectElement(el)) {
      for (const option of Object.values(el.options)) {
        argumentsOf(option.value, into);
      }
    }
    if (isTagElement(el)) {
      argumentsOf(el.children, into);
    }
  }
  return into;
}

/**
 * Parses a message as i18next-icu reads it, with every plural and select
 * needing an other option, as the server requires, or returns why it does
 * not parse.
 */
function parseMessage(
  message: string,
): { ast: MessageFormatElement[] } | { error: string } {
  try {
    return {
      ast: parse(message, { ignoreTag: true, requiresOtherClause: true }),
    };
  } catch (e) {
    return { error: String(e) };
  }
}

/**
 * Returns what is wrong with a set of catalogs: a message that does not
 * parse, an empty message in the source catalog, a key that the source
 * catalog lacks, or a translation that uses an argument that the source
 * message does not.
 */
function check(all: Readonly<Record<string, Catalog>>): string[] {
  const problems: string[] = [];
  const source = all[SOURCE_LOCALE] ?? {};
  for (const [locale, catalog] of Object.entries(all)) {
    for (const [key, message] of Object.entries(catalog)) {
      if (message === "") {
        if (locale === SOURCE_LOCALE) {
          problems.push(`${locale}: ${key}: the message is empty`);
        }
        continue;
      }
      const parsed = parseMessage(message);
      if ("error" in parsed) {
        problems.push(`${locale}: ${key}: ${parsed.error}`);
        continue;
      }
      if (locale === SOURCE_LOCALE) {
        continue;
      }
      const original = source[key];
      if (original === undefined) {
        problems.push(`${locale}: ${key}: the source catalog has no such key`);
        continue;
      }
      const sourceParsed = parseMessage(original);
      if (original === "" || "error" in sourceParsed) {
        // The source message's problem is reported with it, and the
        // translation's arguments cannot be checked against it.
        continue;
      }
      const allowed = argumentsOf(sourceParsed.ast, new Set());
      for (const arg of argumentsOf(parsed.ast, new Set())) {
        if (!allowed.has(arg)) {
          problems.push(
            `${locale}: ${key}: the argument ${arg} is not in the source message`,
          );
        }
      }
    }
  }
  return problems;
}

it("has every catalog file of web/shared/i18n", () => {
  const files = readdirSync(new URL("../../i18n/", import.meta.url))
    .filter((name) => name.endsWith(".json"))
    .map((name) => name.slice(0, -".json".length));
  expect(Object.keys(catalogs).sort()).toEqual(files.sort());
  expect(Object.keys(catalogs)).toContain(SOURCE_LOCALE);
});

it("names every catalog after a locale's canonical BCP 47 tag", () => {
  // matchLocale reads each name as a tag, and would throw on one that is not
  // a tag, such as en_US. The server's catalogs are held to the same.
  for (const name of Object.keys(catalogs)) {
    expect(() => Intl.getCanonicalLocales(name), name).not.toThrow();
    expect(Intl.getCanonicalLocales(name), name).toEqual([name]);
  }
});

it("has valid messages in every catalog", () => {
  expect(check(catalogs)).toEqual([]);
});

it("finds what is wrong with a catalog", () => {
  expect(
    check({
      en: {
        empty: "",
        greeting: "Hello, {name}!",
        files: "{n, plural, one {# file}}",
      },
      cs: {
        greeting: "Ahoj, {nick}!",
        extra: "Navíc",
        unclosed: "{name",
        skipped: "",
        // Translations of source messages that do not parse or are empty,
        // whose problems are the ones reported for their keys.
        files: "{n} souborů",
        empty: "{n} prázdné",
      },
    }),
  ).toEqual([
    "en: empty: the message is empty",
    expect.stringMatching(/^en: files: SyntaxError: MISSING_OTHER_CLAUSE/),
    "cs: greeting: the argument nick is not in the source message",
    "cs: extra: the source catalog has no such key",
    expect.stringMatching(/^cs: unclosed: SyntaxError/),
  ]);
});
