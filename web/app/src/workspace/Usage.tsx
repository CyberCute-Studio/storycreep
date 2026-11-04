// What a workspace uses of what the plans meter, against its limits (PRD
// FR-ENT-02, FR-ENT-03): the settings' table of each, and the note that the
// dialogs that add a project or invite a member show of their limit, which
// warns once the workspace uses 80% of it or more. A limit is never an offer
// of a higher plan: only a refusal of one names where the plans are, which a
// self-hosted instance's never does (RefusalAlert).

import type { LimitUsageBody, UsageBody } from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

/** A limit of the plans that a dialog adds to. */
export type LimitKey = "projects" | "members";

/**
 * The note of how much of its limit of key the workspace uses, with a
 * warning from 80% of it, or nothing where it has no limit of key, as on a
 * self-hosted instance that caps nothing.
 */
export function LimitNote({
  usage,
  limit,
}: {
  usage?: UsageBody;
  limit: LimitKey;
}) {
  const { t } = useTranslation();
  const of = usage?.[limit];
  if (of?.limit === undefined) {
    return null;
  }
  return (
    <p className={of.warning ? "font-medium" : "text-fg-muted"}>
      {limitMessage(t, limit, { ...of, limit: of.limit })}
      {of.warning && ` ${t("usage.near_limit")}`}
    </p>
  );
}

/** Returns how much the workspace uses of its limit of key, in words. */
function limitMessage(
  t: TFunction,
  key: LimitKey,
  of: LimitUsageBody & { limit: number },
): string {
  return key === "projects"
    ? t("usage.projects_of", { used: of.used, limit: of.limit })
    : t("usage.members_of", { used: of.used, limit: of.limit });
}

/** The units of a size, each a thousand of the one before. */
const byteUnits = [
  "byte",
  "kilobyte",
  "megabyte",
  "gigabyte",
  "terabyte",
  "petabyte",
] as const;

/** Returns a size of bytes in the locale's words, in the largest unit that it is at least one of. */
export function formatBytes(locale: string, bytes: number): string {
  let unit = 0;
  let value = bytes;
  while (value >= 1000 && unit < byteUnits.length - 1) {
    value /= 1000;
    unit++;
  }
  return formatNumber(locale, value, {
    style: "unit",
    unit: byteUnits[unit],
    unitDisplay: "short",
    maximumFractionDigits: unit === 0 ? 0 : 1,
  });
}

/**
 * The table of what the workspace uses: of each limit, how much of it, or
 * that it has none, and of what is metered alone, how much.
 */
export function UsageTable({ usage }: { usage: UsageBody }) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language;
  const limited = (key: LimitKey | "storage_bytes", label: string) => {
    const of = usage[key];
    const used =
      key === "storage_bytes"
        ? formatBytes(locale, of.used)
        : formatNumber(locale, of.used);
    return (
      <tr key={key} className="border-t border-border">
        <th scope="row" className="py-2 pe-4 text-start font-medium">
          {label}
        </th>
        <td className="py-2 pe-4">{used}</td>
        <td className="py-2 pe-4">
          {of.limit === undefined
            ? t("usage.no_limit")
            : key === "storage_bytes"
              ? formatBytes(locale, of.limit)
              : formatNumber(locale, of.limit)}
        </td>
        <td className="py-2">{of.warning ? t("usage.near_limit") : ""}</td>
      </tr>
    );
  };
  const metered = (
    key: "boards" | "nodes" | "snapshots" | "api_requests",
    label: string,
  ) => (
    <tr key={key} className="border-t border-border">
      <th scope="row" className="py-2 pe-4 text-start font-medium">
        {label}
      </th>
      <td className="py-2 pe-4">{formatNumber(locale, usage[key].used)}</td>
      <td className="py-2 pe-4">{t("usage.no_limit")}</td>
      <td className="py-2" />
    </tr>
  );
  return (
    <div className="overflow-x-auto">
      <table className="w-full">
        <caption className="sr-only">{t("usage.title")}</caption>
        <thead>
          <tr>
            <th scope="col" className="pb-2 pe-4 text-start text-fg-muted">
              {t("usage.what")}
            </th>
            <th scope="col" className="pb-2 pe-4 text-start text-fg-muted">
              {t("usage.used")}
            </th>
            <th scope="col" className="pb-2 pe-4 text-start text-fg-muted">
              {t("usage.limit")}
            </th>
            <th scope="col" className="pb-2 text-start text-fg-muted">
              <span className="sr-only">{t("usage.warning")}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {limited("projects", t("usage.projects"))}
          {limited("members", t("usage.members"))}
          {limited("storage_bytes", t("usage.storage"))}
          {metered("boards", t("usage.boards"))}
          {metered("nodes", t("usage.nodes"))}
          {metered("snapshots", t("usage.snapshots"))}
          {metered("api_requests", t("usage.api_requests"))}
        </tbody>
      </table>
    </div>
  );
}
