// What the forms of the account screens share: the check that a new
// password was typed the same twice, and the fields of a registration that
// the server's refusals name.

import type { FieldOf } from "./refusal";

/**
 * Reports whether the form's new password, in the field password, differs
 * from its confirmation, in confirm, which the form checks before it asks
 * the server: a password typed wrong once would be one that nobody knows.
 */
export function passwordsDiffer(
  data: FormData,
  password = "password",
  confirm = "confirm",
): boolean {
  return data.get(password) !== data.get(confirm);
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
