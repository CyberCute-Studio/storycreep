// What the forms of the account screens share: the shortest password, the
// check that a new password was typed the same twice, signing in with an
// account just registered, and the fields of a registration that the
// server's refusals name.

import { ApiError } from "@storycreep/shared/api";
import type { FieldOf } from "./refusal";

/**
 * The fewest characters that a password may have, as the server's
 * account.MinPasswordLength, which the forms' hints name, and the server's
 * refusal of a shorter password (PRD FR-AUTH-01).
 */
export const minPasswordLength = 12;

/**
 * A form's own refusal of a new password that differs from its
 * confirmation, which it checks before it asks the server: a password typed
 * wrong once would be one that nobody knows. Each is an object of its own,
 * which useFocusRefusal tells from the one before.
 */
export interface Mismatch {
  /** The field of the confirmation, which shows the refusal. */
  readonly field: string;
}

/**
 * Returns the Mismatch of the form's new password, in the field password,
 * and its confirmation, in confirm, or undefined if they are the same.
 */
export function passwordMismatch(
  data: FormData,
  password = "password",
  confirm = "confirm",
): Mismatch | undefined {
  return data.get(password) === data.get(confirm)
    ? undefined
    : { field: confirm };
}

/**
 * Signs in with an account that the page has just registered (signIn), and
 * resolves to "registered" instead if the server refuses the credentials:
 * it answers a registration alike whether or not the address had an
 * account, so as not to tell which, and one that had, with another
 * password, stays as it was, which the page then asks the user to sign in
 * to.
 */
export async function signInRegistered<T>(
  signIn: () => Promise<T>,
): Promise<T | "registered"> {
  try {
    return await signIn();
  } catch (error) {
    if (
      error instanceof ApiError &&
      error.error?.code === "invalid_credentials"
    ) {
      return "registered";
    }
    throw error;
  }
}

/**
 * Returns the field of a form that a location of the server's names, as
 * body.email, from the fields that the form has under the body.
 */
export function bodyField(...fields: string[]): FieldOf {
  return (location) => {
    const field = location.replace(/^body\./, "");
    return fields.includes(field) ? field : undefined;
  };
}

/** The fields of a registration, which registering with an invitation has too. */
export const registrationField = bodyField("display_name", "email", "password");
