// The header's notice of each project that the device could not keep
// offline because the browser's storage is full (PRD FR-OFF-02 AC2), which
// names the project, and leads to the storage panel, where the user makes
// room, as by removing another project, after which the keeper tries again.
// Its copies are written in the background, as the project changes, so the
// notice is the header's, on every page, rather than the project's alone.

import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { textLink } from "../ui/Page";
import { useOffline } from "./react";

/** The header's notices of the projects that the device's storage could not keep. */
export function OfflineNotices() {
  const { t } = useTranslation();
  const pins = useOffline((s) => s.pins);
  const full = [...pins.values()]
    .filter((p) => p.status === "failed" && p.failure === "quota")
    .map((p) => ({ id: p.projectId, name: p.name }));
  // The alert appears with its first notice, which a screen reader reads as
  // it appears, rather than stand empty in the header beside the header's
  // own, which says that signing out failed.
  if (full.length === 0) {
    return null;
  }
  return (
    <div role="alert" className="border-t border-border px-4 py-3 text-danger">
      <ul>
        {full.map(({ id, name }) => (
          <li key={id}>
            {t("offline.full_notice", { name })}{" "}
            <Link to="/account/offline" className={textLink}>
              {t("offline.full_notice_link")}
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}
