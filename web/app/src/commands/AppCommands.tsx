// The commands of every page of a signed-in account (PRD FR-EDIT-10, plan
// note N72): the palette's, the table of the shortcuts', and those that go
// to the projects and to the account, and the dialogs of the palette and
// of the table, with the header's button that opens the palette, so that
// a finger reaches it too.

import { useNavigate } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { focusIsNowhere } from "../shell/focus";
import { Button } from "../ui/Button";
import { commandOf } from "./catalog";
import { ariaKeyShortcuts } from "./keys";
import { Palette } from "./Palette";
import { useCommandHandlers, usePlatform, useRegistry } from "./registry";
import { Shortcuts } from "./Shortcuts";

/**
 * The control that has the focus, which a dialog that opens gives it back
 * to, or null where none has it (focusIsNowhere), as where a click on a
 * button gives it none, as Safari's and Firefox's on macOS, which leaves
 * it on the document's body, which takes no focus back, so that the dialog
 * gives the page's content the focus as it closes (focusBackTo), rather
 * than leaving it on the body.
 */
function focused(): HTMLElement | null {
  const element = document.activeElement;
  return !focusIsNowhere(document) && element instanceof HTMLElement
    ? element
    : null;
}

/** Registers the app's own commands while signedIn, and shows their dialogs. */
export function AppCommands({ signedIn }: { signedIn: boolean }) {
  const registry = useRegistry();
  const navigate = useNavigate();
  const [palette, setPalette] = useState(false);
  const [shortcuts, setShortcuts] = useState(false);
  const opener = useRef<HTMLElement | null>(null);
  const tableOpener = useRef<HTMLElement | null>(null);
  useCommandHandlers(
    "app",
    signedIn
      ? {
          // The palette's key closes it again in it (Palette), whose keys
          // are its own, as a dialog's are.
          "palette.open": {
            run: () => {
              opener.current = focused();
              setPalette(true);
            },
          },
          "shortcuts.show": {
            run: () => {
              tableOpener.current = focused();
              setShortcuts(true);
            },
          },
          "go.projects": { run: () => void navigate({ to: "/projects" }) },
          "go.account": { run: () => void navigate({ to: "/account" }) },
        }
      : {},
  );
  if (registry === undefined) {
    return null;
  }
  return (
    <>
      <Palette
        registry={registry}
        open={palette}
        opener={opener}
        onOpenChange={setPalette}
      />
      <Shortcuts
        open={shortcuts}
        opener={tableOpener}
        onOpenChange={setShortcuts}
      />
    </>
  );
}

/** The header's button that opens the palette, with its keys, which assistive technology is told of. */
export function CommandsButton() {
  const { t } = useTranslation();
  const registry = useRegistry();
  const platform = usePlatform();
  return (
    <Button
      aria-haspopup="dialog"
      aria-keyshortcuts={ariaKeyShortcuts(
        commandOf("palette.open").keys,
        platform,
      )}
      onClick={() => registry?.run("palette.open")}
    >
      {t("shell.commands")}
    </Button>
  );
}
