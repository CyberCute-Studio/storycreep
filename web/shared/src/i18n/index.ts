// Localisation of the product UI (PRD FR-I18N-01, FR-I18N-02, ADR-024),
// shared by the web app and the public site.
//
// The catalogs are web/shared/i18n/<locale>.json: JSON objects of messages
// by stable key, in ICU MessageFormat, which i18next formats with
// i18next-icu. English is the source locale, whose catalog has every
// message; a message that another locale's catalog lacks is shown in
// English. Code names a message as t("key"), with a string literal, which
// catalog extraction (pnpm i18n:extract) reads to keep the catalogs to the
// keys that the code uses. The server formats its own catalog the same way,
// and schema/i18n/vectors.json holds both to it.

import i18next, { type i18n as I18n, type Resource } from "i18next";
import ICU, { type IcuInstance } from "i18next-icu";
import en from "../../i18n/en.json" with { type: "json" };

export {
  formatDate,
  formatDateTime,
  formatNumber,
  formatRelativeTime,
} from "./format";

/** The source locale, whose catalog has every message. */
export const SOURCE_LOCALE = "en";

/** A catalog: messages in ICU MessageFormat, by key. */
export type Catalog = Readonly<Record<string, string>>;

/**
 * The catalog of each locale, by its BCP 47 tag. A locale that gains a
 * catalog in web/shared/i18n is added here, which a test checks.
 */
export const catalogs: Readonly<Record<string, Catalog>> = { en };

/** What a locale is resolved from, most important first (PRD FR-I18N-02). */
export interface LocaleSources {
  /** The locale that the user chose, if any. */
  preference?: string | null;
  /** The browser's locales, most preferred first, as navigator.languages. */
  accepted?: readonly string[];
  /** The instance's default locale. */
  instanceDefault?: string | null;
}

// Orders locales as the server's Bundle orders its catalogs: the source
// locale first, and then by tag.
function sourceFirst(a: string, b: string): number {
  if (a === b) {
    return 0;
  }
  if (a === SOURCE_LOCALE || b === SOURCE_LOCALE) {
    return a === SOURCE_LOCALE ? -1 : 1;
  }
  return a < b ? -1 : 1;
}

/**
 * Returns the locale of `supported` that `tag` asks for: the same locale,
 * such as en-GB for en-GB, or else the first of the same language in the same
 * script, the source locale first and then by tag, such as en for en-AU, but
 * not zh-Hans for zh-Hant. It returns undefined for a tag that no locale
 * matches, for und, and for a tag that is not a valid BCP 47 tag. The
 * server's Bundle.Match picks the same locale (TestMatchRegion).
 */
export function matchLocale(
  tag: string,
  supported: readonly string[] = Object.keys(catalogs),
): string | undefined {
  let wanted: Intl.Locale;
  try {
    const parsed = new Intl.Locale(tag);
    if (parsed.baseName === "und") {
      // A tag that names no language asks for none.
      return undefined;
    }
    wanted = parsed.maximize();
  } catch {
    return undefined;
  }
  // Both sides are maximized, so that en-GB matches the catalog en-GB, and
  // en-US the catalog en, whose likely region is US.
  const offered = [...supported].sort(sourceFirst).map((locale) => ({
    locale,
    max: new Intl.Locale(locale).maximize(),
  }));
  const same = offered.find((o) => o.max.baseName === wanted.baseName);
  if (same !== undefined) {
    return same.locale;
  }
  return offered.find(
    (o) => o.max.language === wanted.language && o.max.script === wanted.script,
  )?.locale;
}

/**
 * Returns the locale to show the UI in (PRD FR-I18N-02): the first of the
 * user's preference, the browser's locales, and the instance's default that
 * a catalog matches, or else the source locale. A locale without a catalog,
 * or a tag that is not valid, is skipped, so an unknown locale falls back to
 * English without an error.
 */
export function resolveLocale(
  sources: LocaleSources,
  supported: readonly string[] = Object.keys(catalogs),
): string {
  const tags = [
    sources.preference,
    ...(sources.accepted ?? []),
    sources.instanceDefault,
  ];
  for (const tag of tags) {
    const locale = tag ? matchLocale(tag, supported) : undefined;
    if (locale !== undefined) {
      return locale;
    }
  }
  return SOURCE_LOCALE;
}

/** Options of createI18n. */
export interface I18nOptions {
  /** The catalogs to use instead of the shared ones, as in a test. */
  catalogs?: Readonly<Record<string, Catalog>>;
}

// i18next-icu's parse, which its type declarations leave out. info is what
// i18next found: resolved.usedLng is the locale of the catalog that has the
// message.
type IcuParse = (
  res: string,
  options: object,
  lng: string,
  ns: string,
  key: string,
  info?: { resolved?: { usedLng?: string } },
) => string;

/**
 * Returns i18next-icu's module, made to format a message in the locale of the
 * catalog that has it. i18next hands the module the locale that was asked for
 * even for a message that it found in English, whose plural options and
 * numbers would then follow that locale's rules, as "1 200 boards" in Czech.
 * The server formats such a message in English, the locale that its options
 * are written for, and so does this module.
 */
function icuModule(): IcuInstance {
  const icu = new ICU() as IcuInstance & { parse: IcuParse };
  const parse = icu.parse.bind(icu);
  icu.parse = (res, options, lng, ns, key, info) =>
    parse(res, options, info?.resolved?.usedLng ?? lng, ns, key, info);
  return icu;
}

/**
 * Returns an i18next instance that formats the catalogs' messages in the
 * locale that `locale` matches, or else in English, with ICU MessageFormat.
 * Its t takes a key and the message's arguments, as t("files.count", {
 * count: 2 }). A message that the locale's catalog lacks is shown, and
 * formatted, in English. A key that no catalog has is returned as it is, and
 * so is a message that cannot be formatted with the arguments given, as when
 * one is missing. i18next's changeLanguage switches the locale without a
 * reload. Pass it a locale that resolveLocale returned: i18next matches a
 * tag by its language alone, so it would show a zh-TW reader a zh catalog of
 * Simplified Chinese, which matchLocale and the server do not pick for that
 * reader.
 */
export function createI18n(locale: string, options: I18nOptions = {}): I18n {
  const all = options.catalogs ?? catalogs;
  const supported = Object.keys(all);
  const resources: Resource = {};
  for (const [tag, catalog] of Object.entries(all)) {
    resources[tag] = { translation: catalog };
  }
  const instance = i18next.createInstance();
  instance.use(icuModule());
  void instance.init({
    lng: matchLocale(locale, supported) ?? SOURCE_LOCALE,
    fallbackLng: SOURCE_LOCALE,
    supportedLngs: supported,
    resources,
    // Keys are flat: a dot or a colon is part of the key, not a path or a
    // namespace.
    keySeparator: false,
    nsSeparator: false,
    // A message left empty in a translation is missing there.
    returnEmptyString: false,
    returnNull: false,
    // The resources are given, so the instance is ready when init returns.
    initAsync: false,
  });
  return instance;
}
