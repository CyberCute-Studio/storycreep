// The app's localisation (PRD FR-I18N-01, FR-I18N-02): an i18next instance
// of the shared catalogs, which names the page's language and direction on
// its root element, and again whenever the locale changes. The locale is
// resolved from the user's preference, the browser's languages, and the
// instance's default, in that order (applyLocale), and changes without a
// reload when any of them does, as when the user picks another.

import {
  type Catalog,
  createI18n,
  resolveLocale,
} from "@storycreep/shared/i18n";
import type { i18n as I18n } from "i18next";

/**
 * Returns the app's i18next instance, in the locale that languages, the
 * browser's, most preferred first, resolve to, until the user's preference
 * and the instance's default are known, which it names in root's lang and
 * dir. Catalogs other than the shared ones are for tests.
 */
export function createAppI18n(
  languages: readonly string[],
  root: HTMLElement,
  catalogs?: Readonly<Record<string, Catalog>>,
): I18n {
  const i18n = createI18n(
    resolveLocale({ accepted: languages }, catalogs && Object.keys(catalogs)),
    { catalogs },
  );
  const apply = (locale: string) => {
    root.lang = locale;
    root.dir = i18n.dir(locale);
  };
  apply(i18n.language);
  i18n.on("languageChanged", apply);
  return i18n;
}

/** The locales that i18n has a catalog of, the source locale first. */
export function supportedLocales(i18n: I18n): string[] {
  const supported = i18n.options.supportedLngs;
  return (Array.isArray(supported) ? supported : []).filter(
    (locale) => locale !== "cimode",
  );
}

/** What the page's locale is resolved from, besides the browser's languages. */
export interface LocaleState {
  /** The locale that the signed-in user prefers, or none if signed out. */
  preference?: string | null;
  /** The instance's default locale. */
  instanceDefault?: string | null;
}

/**
 * Shows the page in the locale that the user's preference, the browser's
 * languages, and the instance's default resolve to (PRD FR-I18N-02), without
 * a reload: i18next tells the components that read it, which show
 * themselves again in it.
 */
export function applyLocale(
  i18n: I18n,
  state: LocaleState,
  languages: readonly string[] = navigator.languages,
): void {
  const locale = resolveLocale(
    { ...state, accepted: languages },
    supportedLocales(i18n),
  );
  if (locale !== i18n.language) {
    void i18n.changeLanguage(locale);
  }
}
