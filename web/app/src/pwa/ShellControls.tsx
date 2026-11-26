// What the header shows of the app's worker and of installing the app (PRD
// FR-OFF-01, plan note N95): the browser's offer to install the app, while
// it stands, and that a new version is ready, with its reload, which waits
// for the user, and takes neither the focus nor anything typed: the page
// goes on with its own build until the user reloads it (worker.ts).

import { useTranslation } from "react-i18next";
import { Button } from "../ui/Button";
import { type AppShell, useShellState } from "./worker";

/** The header's button that installs the app, while the browser offers to. */
export function InstallButton({ shell }: { shell: AppShell }) {
  const { t } = useTranslation();
  const { installable } = useShellState(shell);
  if (!installable) {
    return null;
  }
  return (
    <Button onClick={() => void shell.install()}>{t("shell.install")}</Button>
  );
}

/**
 * The header's notice of a new version, with its reload, which says to save
 * first: what a form or an editor holds that is not saved goes with the
 * page, as with any reload. It is a status, which a screen reader reads out
 * as it shows, without moving the focus.
 */
export function UpdateNotice({ shell }: { shell: AppShell }) {
  const { t } = useTranslation();
  const { update, reloading } = useShellState(shell);
  return (
    <div role="status">
      {update && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-border px-4 py-2">
          <p>{t("shell.update.body")}</p>
          <Button disabled={reloading} onClick={() => void shell.reload()}>
            {reloading ? t("shell.update.reloading") : t("shell.update.reload")}
          </Button>
        </div>
      )}
    </div>
  );
}
