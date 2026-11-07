// The frame of every page of the app: a link that skips to the page's
// content, the header, which warns of a page that travels unencrypted (PRD
// NFR-SEC-15), names the signed-in account, and signs it out, and the
// content, as the page's main landmark, which gets the focus once the
// control that had it goes (keepFocusOnPage).

import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useRef,
} from "react";
import { useTranslation } from "react-i18next";
import { accountQuery, sessionQuery } from "../api";
import { signOutFailure, useSignOut } from "../auth/signOut";
import { ErrorConsoleButton } from "../errors/ErrorConsole";
import { PRODUCT_NAME } from "../product";
import { Button } from "../ui/Button";
import { Alert } from "../ui/Page";
import { CONTENT_ID, keepFocusOnPage, skipToContent } from "./focus";
import { unencrypted } from "./transport";

/** Whether a component is within the Shell. */
const Framed = createContext(false);

export function Shell({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const session = useQuery(sessionQuery).data;
  const signOut = useSignOut();
  const failure = signOutFailure(signOut, session, t);
  const frame = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const element = frame.current;
    return element === null ? undefined : keepFocusOnPage(element);
  }, []);
  return (
    <Framed.Provider value={true}>
      <div ref={frame} className="flex min-h-dvh flex-col">
        <a
          href={`#${CONTENT_ID}`}
          onClick={skipToContent}
          className="sr-only rounded-md bg-accent px-4 py-2 text-accent-fg focus:not-sr-only focus:fixed focus:start-2 focus:top-2 focus:z-10"
        >
          {t("shell.skip_to_content")}
        </a>
        <header className="border-b border-border bg-surface">
          {unencrypted(window.location) && <UnencryptedWarning />}
          <div className="flex min-h-14 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-1">
            {/* The product's name leads a signed-in user to the project
                list, rather than to the start page, which opens the board
                that the device last showed them, and so, from that board's
                page, would lead nowhere (PRD FR-PROJ-04); and a signed-out
                one to the start page, which signing in goes back to. */}
            <Link
              to={session ? "/projects" : "/"}
              className="flex min-h-11 items-center text-lg font-semibold text-fg"
            >
              {PRODUCT_NAME}
            </Link>
            <div className="ms-auto flex flex-wrap items-center gap-2">
              <SignedInControls signOut={signOut} />
              <ErrorConsoleButton />
            </div>
          </div>
          <Alert
            message={failure}
            className="border-t border-border px-4 py-3 text-danger"
          />
        </header>
        <main id={CONTENT_ID} tabIndex={-1} className="flex-1 px-4 py-8">
          {children}
        </main>
      </div>
    </Framed.Provider>
  );
}

/**
 * The warning of a page served over plain HTTP from another machine, which
 * every page shows at its top, before anything else of it.
 */
function UnencryptedWarning() {
  const { t } = useTranslation();
  return (
    <div className="bg-danger px-4 py-3 text-danger-fg">
      <p>
        <strong>{t("shell.unencrypted.title")}</strong>{" "}
        {t("shell.unencrypted.body")}
      </p>
    </div>
  );
}

/**
 * The signed-in account's link to its settings, and signing out, whose
 * failure the header says under them.
 */
function SignedInControls({
  signOut,
}: {
  signOut: ReturnType<typeof useSignOut>;
}) {
  const { t } = useTranslation();
  const session = useQuery(sessionQuery).data;
  const account = useQuery({ ...accountQuery, enabled: !!session }).data;
  if (!session) {
    return null;
  }
  return (
    <>
      <Link
        to="/account"
        className="flex min-h-11 max-w-48 items-center rounded-md px-2 text-fg underline-offset-2 hover:underline"
      >
        <span className="min-w-0 truncate">
          {account?.display_name ?? t("shell.account")}
        </span>
      </Link>
      <Button
        disabled={signOut.isPending}
        onClick={() => signOut.mutate(session)}
      >
        {t("shell.sign_out")}
      </Button>
    </>
  );
}

/**
 * Shows children within the Shell, which frames them already if they are
 * within it, or else around them, as for a page that a route's boundary
 * shows in place of the root route's component, the Shell itself.
 */
export function InShell({ children }: { children: ReactNode }) {
  return useContext(Framed) ? children : <Shell>{children}</Shell>;
}
