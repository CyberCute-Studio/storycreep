// The frame of a page's content: its heading, which names the page in the
// browser's title too (useDocumentTitle), and the column it is set in, and
// the messages that a form or a page shows of what happened.

import { Link, type LinkProps } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useDocumentTitle } from "./title";

export interface PageProps {
  title: string;
  /** narrow for a form, as signing in; wide for a list, as the sessions. */
  width?: "narrow" | "wide";
  children: ReactNode;
}

/** A page of the app, with its heading. */
export function Page({ title, width = "narrow", children }: PageProps) {
  useDocumentTitle(title);
  return (
    <div
      className={`mx-auto space-y-6 ${width === "narrow" ? "max-w-md" : "max-w-3xl"}`}
    >
      <h1 className="text-2xl font-semibold">{title}</h1>
      {children}
    </div>
  );
}

/**
 * Where a form says that it was refused. The live region is on the page
 * before its message, so that a screen reader reads each message that
 * appears in it.
 */
export function Alert({ message }: { message?: string }) {
  return (
    <div role="alert">
      {message !== undefined && (
        <p className="rounded-md border border-danger px-4 py-3 text-danger">
          {message}
        </p>
      )}
    </div>
  );
}

/** Where a form says that what it asked for was done, as a live region. */
export function Status({ message }: { message?: string }) {
  return (
    <div role="status">
      {message !== undefined && (
        <p className="rounded-md border border-border bg-surface px-4 py-3">
          {message}
        </p>
      )}
    </div>
  );
}

/** The classes of a link in text, which hover changes the colour of alone. */
export const textLink =
  "text-accent underline underline-offset-2 hover:text-accent-hover";

/** A link to a page of the app, in text. */
export function TextLink(props: LinkProps & { children: ReactNode }) {
  return <Link {...props} className={textLink} />;
}
