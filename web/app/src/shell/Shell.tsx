// The frame of every page of the app: a link that skips to the page's
// content, the header, and the content, as the page's main landmark.

import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { ErrorConsoleButton } from "../errors/ErrorConsole";
import { PRODUCT_NAME } from "../product";
import { CONTENT_ID } from "./focus";

export function Shell({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-dvh flex-col">
      <a
        href={`#${CONTENT_ID}`}
        className="sr-only rounded-md bg-accent px-4 py-2 text-accent-fg focus:not-sr-only focus:fixed focus:start-2 focus:top-2 focus:z-10"
      >
        {t("shell.skip_to_content")}
      </a>
      <header className="flex min-h-14 items-center gap-4 border-b border-border bg-surface px-4">
        <Link
          to="/"
          className="flex min-h-11 items-center text-lg font-semibold text-fg"
        >
          {PRODUCT_NAME}
        </Link>
        <div className="ms-auto">
          <ErrorConsoleButton />
        </div>
      </header>
      <main id={CONTENT_ID} tabIndex={-1} className="flex-1 px-4 py-8">
        {children}
      </main>
    </div>
  );
}
