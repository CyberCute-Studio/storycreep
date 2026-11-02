// What a form shows of a request that failed: the server's refusal, in the
// words of the app's catalog where the app knows its code, at the form's
// field that the server names, as a password that is too short, or for the
// whole form, as a wrong password at sign-in. A refusal whose code the app
// does not know is shown in the server's own words, which it writes in the
// locale that the request asks for, the page's (api.ts, PRD FR-I18N-01).

import { ApiError, NetworkError } from "@storycreep/shared/api";
import type { TFunction } from "i18next";
import { minPasswordLength } from "./forms";

/** What a form shows of a failed request. */
export interface Refusal {
  /** What the form shows for itself, if anything. */
  message?: string;
  /** What it shows at each of its fields, by the field's name. */
  fields: Record<string, string>;
}

/**
 * Returns the field of a form that a location of the server's names, as
 * body.new_password, or undefined if the form has none, when the problem is
 * shown for the whole form.
 */
export type FieldOf = (location: string) => string | undefined;

/** A problem of an answer of validation_failed, weak_password, and the like. */
interface Problem {
  location: string;
  message: string;
}

/** The error codes whose refusals are at a field of their own. */
const codeLocations: Readonly<Record<string, string>> = {
  invalid_setup_code: "body.setup_code",
};

/**
 * Returns what a form shows of error, which a request of the form failed
 * with. fieldOf names the form's fields that the server's locations name.
 */
export function refusalOf(
  error: unknown,
  t: TFunction,
  fieldOf: FieldOf = () => undefined,
): Refusal {
  if (!(error instanceof ApiError)) {
    // A fault of the app's, as a TypeError, is unexpected, and the error
    // console records it.
    return {
      message:
        error instanceof NetworkError
          ? t("error.network")
          : t("error.unexpected"),
      fields: {},
    };
  }
  const body = error.error;
  if (body === undefined || error.status >= 500) {
    return { message: t("error.server"), fields: {} };
  }
  const fields: Record<string, string> = {};
  const unplaced: string[] = [];
  const own = codeMessage(body.code, body.details ?? {}, error.response, t);
  for (const problem of problemsOf(body.details, codeLocations[body.code])) {
    const field = fieldOf(problem.location);
    const message =
      problemMessage(body.code, body.details ?? {}, t) ??
      sentence(problem.message);
    if (field === undefined) {
      unplaced.push(message);
    } else {
      fields[field] ??= message;
    }
  }
  const placed = Object.keys(fields).length > 0;
  const message =
    own ??
    (unplaced.length > 0
      ? unplaced.join(" ")
      : placed
        ? undefined
        : body.message);
  return message === undefined ? { fields } : { message, fields };
}

/** Returns the problems of an answer's details, or one at location if it has none. */
function problemsOf(
  details: Record<string, unknown> | undefined,
  location: string | undefined,
): Problem[] {
  const listed = Array.isArray(details?.errors) ? details.errors : [];
  const problems = listed.filter(
    (p): p is Problem =>
      typeof p?.location === "string" && typeof p?.message === "string",
  );
  if (problems.length === 0 && location !== undefined) {
    return [{ location, message: "" }];
  }
  return problems;
}

/**
 * Returns the app's message of a refusal of a field whose code says what is
 * wrong, or undefined for one whose own message says it, as validation_failed.
 */
function problemMessage(
  code: string,
  details: Record<string, unknown>,
  t: TFunction,
): string | undefined {
  switch (code) {
    case "weak_password":
      return details.reason === "too_short"
        ? t("auth.error.password_too_short", { min: minPasswordLength })
        : t("auth.error.password_common");
    case "wrong_password":
      return t("auth.error.wrong_password");
    case "invalid_setup_code":
      return t("setup.error.code");
    case "slug_taken":
      return t("setup.error.slug_taken");
  }
  return undefined;
}

/** Returns the app's message of a refusal of the whole request, if it has one. */
function codeMessage(
  code: string,
  details: Record<string, unknown>,
  response: Response,
  t: TFunction,
): string | undefined {
  switch (code) {
    case "invalid_credentials":
      return t("auth.error.invalid_credentials");
    case "rate_limited": {
      const seconds = Number.parseInt(
        response.headers.get("Retry-After") ?? "",
        10,
      );
      if (!Number.isFinite(seconds)) {
        return t("error.rate_limited_soon");
      }
      return details.reason === "locked"
        ? t("auth.error.locked", { seconds })
        : t("error.rate_limited", { seconds });
    }
    case "csrf_failed":
      return t("error.csrf_failed");
    case "setup_required":
      return t("auth.error.setup_required");
    case "signup_closed":
      return t("auth.error.signup_closed");
    case "invite_required":
      return t("auth.error.invite_required");
    case "email_not_configured":
      return t("auth.error.email_not_configured");
    case "token_invalid":
      return t("auth.error.token_invalid");
    case "token_used":
      return t("auth.error.token_used");
    case "token_expired":
      return t("auth.error.token_expired");
    case "invite_revoked":
      return t("invite.error.revoked");
    case "invite_accepted":
      return t("invite.error.accepted");
    case "invite_expired":
      return t("invite.error.expired");
    case "already_member":
      return t("invite.error.already_member");
    case "limit_exceeded":
      return t("error.limit_exceeded", {
        key: String(details.key ?? ""),
        usage: Number(details.usage ?? 0),
        limit: Number(details.limit ?? 0),
      });
  }
  return undefined;
}

/** Returns a problem's message of the server's, as "is too long", as a sentence. */
function sentence(message: string): string {
  const s = message.trim();
  if (s === "") {
    return s;
  }
  const first = s.charAt(0).toLocaleUpperCase();
  return `${first}${s.slice(1)}${/[.!?]$/.test(s) ? "" : "."}`;
}
