// The storage panel (PRD FR-OFF-02, plan note N97), a tab of the account's
// screens: the projects that the device keeps offline for the account, each
// with why, how much it takes, about, as of when, and its removal, which
// takes its copy off the device at once (AC3); how much the browser lets
// the site keep, and how much it does; and whether the browser keeps the
// data until the user removes it, which the panel asks for again, as
// making a project available offline does (navigator.storage.persist).

import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "../ui/Button";
import { Status, TextLink } from "../ui/Page";
import { formatBytes } from "../workspace/Usage";
import type { OfflineKeeper } from "./keeper";
import { pinStatus } from "./OfflineSwitch";
import { useOnline } from "./online";
import { useKeeper, useOffline } from "./react";

/** What the browser says of the site's storage: how much it uses, of how much, and whether it keeps it. */
interface Storage {
  usage?: number;
  quota?: number;
  persisted?: boolean;
}

/** Reads what the browser says of the site's storage, again each time that keeper's copies change. */
function useStorage(keeper: OfflineKeeper | undefined): {
  storage: Storage;
  persist: () => Promise<void>;
} {
  const [storage, setStorage] = useState<Storage>({});
  const manager =
    typeof navigator === "undefined" ? undefined : navigator.storage;
  const read = useCallback(async () => {
    if (manager === undefined) {
      return;
    }
    const [estimate, persisted] = await Promise.all([
      manager.estimate?.().catch(() => undefined),
      manager.persisted?.().catch(() => undefined),
    ]);
    setStorage({
      usage: estimate?.usage,
      quota: estimate?.quota,
      persisted,
    });
  }, [manager]);
  // What the site uses changes as the copies do.
  useEffect(() => {
    void read();
    return keeper?.store.subscribe(() => void read());
  }, [keeper, read]);
  const persist = useCallback(async () => {
    await manager?.persist?.().catch(() => false);
    await read();
  }, [manager, read]);
  return { storage, persist };
}

/** The panel of what the device keeps offline for the account. */
export function StoragePanel() {
  const { t, i18n } = useTranslation();
  const keeper = useKeeper();
  const available = useOffline((s) => s.available);
  const pins = useOffline((s) => s.pins);
  const online = useOnline();
  const { storage, persist } = useStorage(keeper);
  const [asked, setAsked] = useState(false);
  if (keeper === undefined || available === false) {
    return (
      <p>
        {t(
          keeper?.options.secure === false
            ? "offline.unavailable_insecure"
            : "offline.unavailable_storage",
        )}
      </p>
    );
  }
  const list = [...pins.values()].sort((a, b) => a.name.localeCompare(b.name));
  const bytes = (n: number) => formatBytes(i18n.language, n);
  return (
    <div className="space-y-6">
      <p>{t("offline.panel.intro")}</p>
      <section aria-labelledby="offline-projects" className="space-y-3">
        <h2 id="offline-projects" className="text-xl font-semibold">
          {t("offline.panel.projects")}
        </h2>
        {list.length === 0 ? (
          <p>{t("offline.panel.none")}</p>
        ) : (
          <ul className="space-y-3">
            {list.map((pin) => (
              <li
                key={pin.projectId}
                className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-md border border-border px-4 py-3"
              >
                <div className="min-w-0 flex-1 space-y-1">
                  <p className="font-semibold">
                    <TextLink
                      to="/projects/$projectId"
                      params={{ projectId: pin.projectId }}
                    >
                      {pin.name}
                    </TextLink>
                  </p>
                  <p className="text-sm">
                    {t(
                      pin.chosen
                        ? "offline.panel.chosen"
                        : "offline.panel.last_opened",
                    )}{" "}
                    {pin.copy === undefined
                      ? ""
                      : t("offline.panel.size", {
                          size: bytes(pin.copy.bytes),
                        })}
                  </p>
                  <p className="text-sm text-fg-muted">
                    {pinStatus(
                      { ...pin, auto: false },
                      online,
                      t,
                      i18n.language,
                    )}
                  </p>
                </div>
                <Button
                  onClick={() => void keeper.unpin(pin.projectId)}
                  aria-label={t("offline.panel.remove_named", {
                    name: pin.name,
                  })}
                >
                  {t("offline.panel.remove")}
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-labelledby="offline-storage" className="space-y-3">
        <h2 id="offline-storage" className="text-xl font-semibold">
          {t("offline.panel.storage")}
        </h2>
        {storage.usage !== undefined && storage.quota !== undefined && (
          <p>
            {t("offline.panel.usage", {
              usage: bytes(storage.usage),
              quota: bytes(storage.quota),
            })}
          </p>
        )}
        <p>
          {t(
            storage.persisted
              ? "offline.panel.persisted"
              : "offline.panel.not_persisted",
          )}
        </p>
        {storage.persisted !== true && (
          <Button
            onClick={() =>
              void persist().then(() => {
                setAsked(true);
              })
            }
          >
            {t("offline.panel.persist")}
          </Button>
        )}
        <Status
          message={
            asked && storage.persisted === false
              ? t("offline.panel.persist_refused")
              : undefined
          }
        />
      </section>
    </div>
  );
}
