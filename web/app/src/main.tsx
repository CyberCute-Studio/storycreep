// The app's entry point: it records the page's errors from the start, sets
// up the locale, the server's state, and the router, and renders the app.

import { isNotFound, isRedirect } from "@tanstack/react-router";
import { setNonce } from "get-nonce";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { createQueryClient } from "./api";
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
const queryClient = createQueryClient(errorLog);
const router = createAppRouter({ queryClient });

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
