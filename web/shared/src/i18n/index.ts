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
import ICU from "i18next-icu";
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

/**
 * Returns the locale of `supported` that `tag` asks for: the same language
 * in the same script, such as en for en-GB, but not zh-Hans for zh-Hant. It
 * returns undefined for a tag that no locale matches, or that is not a
 * valid BCP 47 tag.
 */
export function matchLocale(
  tag: string,
  supported: readonly string[] = Object.keys(catalogs),
): string | undefined {
  let wanted: Intl.Locale;
  try {
    wanted = new Intl.Locale(tag).maximize();
  } catch {
    return undefined;
  }
  const exact = supported.find(
    (locale) => locale.toLowerCase() === wanted.baseName.toLowerCase(),
  );
  if (exact !== undefined) {
    return exact;
  }
  return supported.find((locale) => {
    const offered = new Intl.Locale(locale).maximize();
    return (
      offered.language === wanted.language && offered.script === wanted.script
    );
  });
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

/**
 * Returns an i18next instance that formats the catalogs' messages in the
 * locale that `locale` matches, or else in English, with ICU MessageFormat.
 * Its t takes a key and the message's arguments, as t("files.count", {
 * count: 2 }). A key that no catalog has is returned as it is, and so is a
 * message that cannot be formatted with the arguments given, as when one is
 * missing. i18next's changeLanguage switches the locale without a reload.
 */
export function createI18n(locale: string, options: I18nOptions = {}): I18n {
  const all = options.catalogs ?? catalogs;
  const supported = Object.keys(all);
  const resources: Resource = {};
  for (const [tag, catalog] of Object.entries(all)) {
    resources[tag] = { translation: catalog };
  }
  const instance = i18next.createInstance();
  instance.use(ICU);
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
