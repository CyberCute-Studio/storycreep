// The app's entry point: it records the page's errors before anything else
// of it runs, sets up the locale, the server's state, and the router, and
// renders the app. Only the modules that it imports run before, as they
// load: an error that one of them throws then stops them all, and this file
// too, so that no console could show it, while what they leave to run later
// runs after this file has run.

import { isNotFound, isRedirect } from "@tanstack/react-router";
import { setNonce } from "get-nonce";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import {
  createQueryClient,
  requestsIn,
  requestVersion,
  sessionQuery,
  whenSaved,
} from "./api";
import { followSession, sessionChanges } from "./auth/session";
import { Documents } from "./document/documents";
import { createRealtime, socketURL } from "./document/realtime";
import { captureGlobalErrors, rootErrorOptions } from "./errors/capture";
import { errorLog } from "./errors/log";
import { createAppI18n } from "./i18n";
import { cspNonce } from "./nonce";
import { copySource, OfflineDatabase } from "./offline/db";
import { OfflineKeeper, tabsBroadcast, writerLock } from "./offline/keeper";
import { keepReads } from "./offline/kept";
import { onConnection, online } from "./offline/online";
import { startShell } from "./pwa/worker";
import { createAppRouter } from "./router";
import "./styles.css";

captureGlobalErrors(window, errorLog);

// Radix's dialogs add a style to the page, which the Content-Security-Policy
// allows only with the page's nonce.
const nonce = cspNonce(document);
if (nonce !== undefined) {
  setNonce(nonce);
}

const i18n = createAppI18n(navigator.languages, document.documentElement);
requestsIn(i18n);
// A request that the server answers with 401 tells that the page's session
// may have ended elsewhere, as when it was revoked, or that it went with a
// session that the page has replaced since, as by changing the password,
// and one answered with 403 csrf_failed that another tab gave the session
// a new CSRF token, by changing the password: the page asks the server, as
// it does whenever its tab is shown again (sessionDoubtedBy).
const queryClient = createQueryClient(errorLog, {
  checkSession: () => void session.check(),
});
// The real-time channel of the page's projects (PRD FR-RT-01), whose
// operations their documents apply as they commit.
const realtime = createRealtime({
  url: () => socketURL(window.location),
  checkSession: () => session.check(),
  signedIn: () => !!queryClient.getQueryData(sessionQuery.queryKey),
  log: errorLog,
  window,
});
// What the device keeps of the projects that the account keeps offline
// (PRD FR-OFF-02), which a project's document starts from, and which the
// page's queries read once the server does not answer, as offline, in a
// secure context alone, which a page over plain HTTP is not (FR-OFF-01).
const secure = window.isSecureContext;
const offlineDb = new OfflineDatabase();
const documents = new Documents({
  queryClient,
  realtime,
  log: errorLog,
  checkSession: () => void session.check(),
  storage: tabStorage(),
  copies: copySource(offlineDb, secure),
  online: () => online(),
});
const offline = new OfflineKeeper({
  queryClient,
  documents,
  log: errorLog,
  db: offlineDb,
  secure,
  online: () => online(),
  onConnection: (listener) => onConnection(listener),
  lock: writerLock(navigator),
  broadcast: secure ? tabsBroadcast() : undefined,
  storage: navigator.storage,
});
if (secure) {
  keepReads(queryClient, offline.reads);
}
/**
 * How long, in milliseconds, a reload for a new version of the app waits
 * at most for the page's batches and saves, which the server answers in far
 * less.
 */
const reloadWait = 10_000;

// The app's service worker, which the build has, and the browser's offer
// to install the app (PRD FR-OFF-01). Its reload for a new version waits a
// while for the batches that the page is sending, and for the saves of its
// forms, as of a project's settings.
const pwa = startShell(window, {
  register: import.meta.env.PROD,
  beforeReload: async () => {
    await Promise.all([
      documents.whenSent(reloadWait),
      whenSaved(queryClient, reloadWait),
    ]);
  },
});
const router = createAppRouter({
  queryClient,
  i18n,
  checkSession: () => session.check(),
  documents,
  realtime,
  pwa,
  offline,
});
// A page whose session has ended elsewhere, or that another tab signed in
// to another account, or to one, shows itself again, through the guards of
// its routes (sessionChanges).
const session = followSession(
  queryClient,
  document,
  sessionChanges(queryClient, router),
);
// A page that started offline from the session that the device keeps, which
// has no CSRF token, asks the server for its own once the browser is online
// again, and so does any other, whose session may have ended meanwhile.
onConnection((connected) => {
  if (connected) {
    void session.check();
  }
});
offline.start();
/**
 * Returns the tab's session storage, which keeps the undo histories over a
 * reload (PRD FR-EDIT-08 AC3), or undefined where the browser refuses it, as
 * with the site's data blocked, where they last as long as the page.
 */
function tabStorage(): Storage | undefined {
  try {
    return window.sessionStorage;
  } catch {
    return undefined;
  }
}

// What the error console's reports name, asked for before anything is shown.
requestVersion(queryClient);

const container = document.getElementById("root");
if (container === null) {
  throw new Error("index.html has no element with the ID root.");
}
createRoot(
  container,
  // The router throws these to its boundaries to show a page, not as errors.
  rootErrorOptions(errorLog, (error) => isNotFound(error) || isRedirect(error)),
).render(
  <StrictMode>
    <App i18n={i18n} queryClient={queryClient} router={router} log={errorLog} />
  </StrictMode>,
);
