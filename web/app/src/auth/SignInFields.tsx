// The fields of signing in, which the sign-in screen and an invitation's
// page, which signs in to accept it, share.

import type { LoginBody } from "@storycreep/shared/api";
import { useTranslation } from "react-i18next";
import { TextField } from "../ui/Field";

/**
 * The email address, which email fills in, as the address that an
 * invitation is for, and the password, which a password manager fills in.
 */
export function SignInFields({ email }: { email?: string }) {
  const { t } = useTranslation();
  return (
    <>
      <TextField
        label={t("auth.email")}
        name="email"
        type="email"
        autoComplete="username"
        defaultValue={email}
        required
      />
      <TextField
        label={t("auth.password")}
        name="password"
        type="password"
        autoComplete="current-password"
        required
      />
    </>
  );
}

/** Returns the credentials that the SignInFields of a form's data hold. */
export function credentialsOf(data: FormData): LoginBody {
  return {
    email: String(data.get("email")),
    password: String(data.get("password")),
  };
}
