// The frame of every page of the app: a link that skips to the page's
// content, the header, which warns of a page that travels unencrypted (PRD
// NFR-SEC-15), and says then that the app runs online alone, with a link to
// how to serve it over HTTPS (FR-OFF-01), offers to install the app while
// the browser does, and says that a new version is ready, and which project
// the device's storage could not keep offline (FR-OFF-02), opens the command
// palette (FR-EDIT-10), leads to the account's notifications with how many
// are unread (FR-COMMENT-02), names the signed-in account, and signs it
// out, and the content, as the page's
// main landmark, which gets the focus once the control that had it goes
// (keepFocusOnPage). The page's commands are registered within it
// (CommandsProvider), whose keys act wherever the focus is on the page, and
// it listens to the account's notifications over the real-time channel
// while the page is signed in (useNotificationListener).

import { useQuery } from "@tanstack/react-query";
import { Link, useRouter } from "@tanstack/react-router";
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
import { AppCommands, CommandsButton } from "../commands/AppCommands";
import { CommandsProvider } from "../commands/registry";
import { ErrorConsoleButton } from "../errors/ErrorConsole";
import { NotificationsLink } from "../notifications/NotificationsLink";
import { useNotificationListener } from "../notifications/queries";
import { OfflineNotices } from "../offline/OfflineNotices";
import { SignOutButton } from "../offline/SignOut";
import { PRODUCT_NAME, TLS_DOCS_URL } from "../product";
import { InstallButton, UpdateNotice } from "../pwa/ShellControls";
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
  const pwa = useRouter().options.context.pwa;
  useEffect(() => {
    const element = frame.current;
    return element === null ? undefined : keepFocusOnPage(element);
  }, []);
  useNotificationListener();
  return (
    <Framed.Provider value={true}>
      <CommandsProvider>
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
                {pwa && <InstallButton shell={pwa} />}
                <SignedInControls signOut={signOut} />
                <ErrorConsoleButton />
              </div>
            </div>
            {pwa && <UpdateNotice shell={pwa} />}
            <OfflineNotices />
            <Alert
              message={failure}
              className="border-t border-border px-4 py-3 text-danger"
            />
          </header>
          <main id={CONTENT_ID} tabIndex={-1} className="flex-1 px-4 py-8">
            {children}
          </main>
        </div>
        <AppCommands signedIn={!!session} />
      </CommandsProvider>
    </Framed.Provider>
  );
}

/**
 * The warning of a page served over plain HTTP from another machine, which
 * every page shows at its top, before anything else of it: such a page is
 * not a secure context, where browsers give it no service worker, so it
 * says too that installing the app, keeping projects offline, and editing
 * offline are unavailable, and links to how to serve the instance over
 * HTTPS (PRD FR-OFF-01), in a new tab, which is not told the instance's
 * address.
 */
function UnencryptedWarning() {
  const { t } = useTranslation();
  return (
    <div className="bg-danger px-4 py-3 text-danger-fg">
      <p>
        <strong>{t("shell.unencrypted.title")}</strong>{" "}
        {t("shell.unencrypted.body")}{" "}
        <a
          href={TLS_DOCS_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="underline underline-offset-2"
        >
          {t("shell.unencrypted.tls")}
        </a>
      </p>
    </div>
  );
}

/**
 * The signed-in account's link to its notifications, its link to its
 * settings, and signing out, whose failure the header says under them.
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
      <CommandsButton />
      <NotificationsLink />
      <Link
        to="/account"
        className="flex min-h-11 max-w-48 items-center rounded-md px-2 text-fg underline-offset-2 hover:underline"
      >
        <span className="min-w-0 truncate">
          {account?.display_name ?? t("shell.account")}
        </span>
      </Link>
      {/* Asks first where the device keeps changes that the server has yet
      to answer (PRD FR-OFF-03). */}
      <SignOutButton signOut={signOut} session={session}>
        {t("shell.sign_out")}
      </SignOutButton>
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
