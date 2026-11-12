// The keys of a command, as the palette and the table of the shortcuts
// write them, each a kbd element, for the platform whose keys the page
// follows: Cmd on macOS, Ctrl elsewhere.

import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import { type Binding, bindingOn, bindingParts } from "./keys";
import { usePlatform } from "./registry";

/** The bindings of a command, on the page's platform, or the first of them alone, as the palette shows it. */
export function KeyCaps({
  bindings,
  first = false,
}: {
  bindings: readonly Binding[];
  first?: boolean;
}) {
  const { t } = useTranslation();
  const platform = usePlatform();
  const on = bindings.filter((b) => bindingOn(b, platform));
  const shown = first ? on.slice(0, 1) : on;
  if (shown.length === 0) {
    return null;
  }
  return (
    <span className="flex shrink-0 flex-wrap items-center gap-1 text-sm text-fg-muted">
      {shown.map((binding, i) => (
        <Fragment key={bindingParts(t, binding, platform).join("+")}>
          {i > 0 && <span>{t("shortcuts.or")}</span>}
          <kbd className="rounded border border-border bg-surface px-1.5 py-0.5 font-sans">
            {bindingParts(t, binding, platform).join("+")}
          </kbd>
        </Fragment>
      ))}
    </span>
  );
}
