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
import { createQueryClient, requestsIn, requestVersion } from "./api";
import { followSession, sessionChanges } from "./auth/session";
import { captureGlobalErrors, rootErrorOptions } from "./errors/capture";
import { errorLog } from "./errors/log";
import { createAppI18n } from "./i18n";
import { cspNonce } from "./nonce";
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
const router = createAppRouter({
  queryClient,
  i18n,
  checkSession: () => session.check(),
});
// A page whose session has ended elsewhere, or that another tab signed in
// to another account, or to one, shows itself again, through the guards of
// its routes (sessionChanges).
const session = followSession(
  queryClient,
  document,
  sessionChanges(queryClient, router),
);
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
