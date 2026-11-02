// The app's localisation (PRD FR-I18N-01, FR-I18N-02): an i18next instance
// of the shared catalogs, in the locale of the browser's languages, which
// names the page's language and direction on its root element, and again
// whenever the locale changes. The user's preference, and the instance's
// default, join the locale's sources with the account screens (plan item
// 54).

import { createI18n, resolveLocale } from "@storycreep/shared/i18n";
import type { i18n as I18n } from "i18next";

/**
 * Returns the app's i18next instance, in the locale that languages, the
 * browser's, most preferred first, resolve to, which it names in root's
 * lang and dir.
 */
export function createAppI18n(
  languages: readonly string[],
  root: HTMLElement,
): I18n {
  const i18n = createI18n(resolveLocale({ accepted: languages }));
  const apply = (locale: string) => {
    root.lang = locale;
    root.dir = i18n.dir(locale);
  };
  apply(i18n.language);
  i18n.on("languageChanged", apply);
  return i18n;
}
