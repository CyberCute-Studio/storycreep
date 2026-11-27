// The switch of a project's pages that makes the project available offline
// (PRD FR-OFF-02), with what the device keeps of it: whether it downloads,
// as of when the copy is, why it is kept, and why it could not be. Over
// plain HTTP, where browsers give a page no service worker and no
// persistent storage, it is unavailable, and says so (FR-OFF-01), as the
// header's warning does with the link to how to serve the instance over
// HTTPS.

import { formatDateTime } from "@storycreep/shared/i18n";
import type { TFunction } from "i18next";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { CheckboxField } from "../ui/Field";
import type { PinView } from "./keeper";
import { useOnline } from "./online";
import { useKeeper, useOffline } from "./react";

/**
 * Returns what the device keeps of pin, as the switch and the storage panel
 * say it, in the locale of t's: a short line, which the switch's hint holds
 * whatever the status, so that the page under it does not move as the
 * status changes, as while the user drags in it.
 */
export function pinStatus(
  pin: PinView | undefined,
  online: boolean,
  t: TFunction,
  locale: string,
): string {
  if (pin === undefined) {
    return t("offline.status.none");
  }
  const time =
    pin.copy === undefined
      ? undefined
      : formatDateTime(locale, new Date(pin.copy.savedAt));
  let status: string;
  if (pin.status === "failed") {
    const full = pin.failure === "quota";
    status =
      time === undefined
        ? t(full ? "offline.status.full" : "offline.status.failed")
        : t(
            full
              ? "offline.status.full_behind"
              : "offline.status.failed_behind",
            {
              time,
            },
          );
  } else if (pin.status === "ready" && time !== undefined) {
    status = t(
      pin.auto && !pin.chosen
        ? "offline.status.ready_last"
        : "offline.status.ready",
      { time },
    );
  } else {
    status = t(
      online ? "offline.status.downloading" : "offline.status.waiting",
    );
  }
  return status;
}

/** The project of projectId's switch of keeping it offline, named name. */
export function OfflineSwitch({
  projectId,
  name,
}: {
  projectId: string;
  name: string;
}) {
  const { t, i18n } = useTranslation();
  const keeper = useKeeper();
  const available = useOffline((s) => s.available);
  const pin = useOffline((s) => s.pins.get(projectId));
  const online = useOnline();
  // What the user asked for, which the switch shows at once, until the
  // device's record of it, which follows a moment after, says it: of the
  // last change asked for, as the user may switch it on and off again
  // before the first has been recorded.
  const [asked, setAsked] = useState<boolean>();
  const changes = useRef(0);
  if (keeper === undefined) {
    return null;
  }
  const hint =
    available === false
      ? t(
          keeper.options.secure
            ? "offline.unavailable_storage"
            : "offline.unavailable_insecure",
        )
      : pinStatus(pin, online, t, i18n.language);
  return (
    <CheckboxField
      label={t("offline.switch")}
      hint={hint}
      checked={asked ?? pin?.chosen ?? false}
      disabled={available !== true}
      onChange={(event) => {
        const chosen = event.currentTarget.checked;
        const change = ++changes.current;
        setAsked(chosen);
        void (chosen ? keeper.pin(projectId, name) : keeper.unchoose(projectId))
          .catch(() => undefined)
          .finally(() => {
            if (changes.current === change) {
              setAsked(undefined);
            }
          });
      }}
    />
  );
}
