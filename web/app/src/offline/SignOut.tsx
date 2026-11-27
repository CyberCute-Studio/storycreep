// Signing out of a page whose device keeps changes that the server has yet
// to answer (PRD FR-OFF-03, plan note N98): the device keeps them for the
// account, across a session that ended by itself and another account's
// signing in, until they are sent; signing out asks first, naming how many
// there are, of every project of the account's, and offers them as JSON,
// for support, before it takes them off the device with the account's
// copies, once the server has signed the page out. Signing out of a device
// that keeps none asks nothing.

import type { SignedInBody } from "@storycreep/shared/api";
import { AlertDialog } from "radix-ui";
import { type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import type { useSignOut } from "../auth/signOut";
import { Button } from "../ui/Button";
import { ConfirmButtons, ConfirmFrame } from "../ui/Dialog";
import { opensWithin, openWait, type QueueRecord } from "./db";
import {
  type ExportedBatch,
  exportFileName,
  pendingExport,
  saveJson,
} from "./pending";
import { useKeeper } from "./react";

/** Returns the changes of records, as the JSON file of pending changes holds them. */
function exportedOfRecords(records: readonly QueueRecord[]): ExportedBatch[] {
  return records.map((record) => ({
    project_id: record.project_id,
    id: record.id,
    made_at: record.time,
    state: record.state === "review" ? "review" : "queued",
    sent: record.sent,
    commands: record.commands,
    ...(record.refusal === undefined ? {} : { refusal: record.refusal }),
  }));
}

/**
 * The button that signs session's page out by signOut, which asks first
 * where the device keeps changes of the account's that the server has yet
 * to answer, and then takes them off the device too; children are its
 * label.
 */
export function SignOutButton({
  signOut,
  session,
  children,
}: {
  signOut: ReturnType<typeof useSignOut>;
  session: SignedInBody;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const keeper = useKeeper();
  // The account's changes that the device keeps, once the click has found
  // some, while the dialog that asks is open.
  const [asked, setAsked] = useState<QueueRecord[]>();
  const [reading, setReading] = useState(false);
  const db = keeper?.available ? keeper.db : undefined;
  const userId = session.user_id;
  const end = async (records: readonly QueueRecord[]) => {
    try {
      await signOut.mutateAsync(session);
    } catch {
      // The header says why the page is still signed in (signOutFailure).
      return;
    }
    setAsked(undefined);
    if (records.length > 0) {
      // The user decided: the changes go with the account's copies.
      await db?.deleteQueued(userId).catch(() => undefined);
    }
  };
  const click = async () => {
    if (db === undefined) {
      await end([]);
      return;
    }
    setReading(true);
    let records: QueueRecord[] = [];
    try {
      records = (await opensWithin(db, openWait))
        ? await db.queued(userId).catch(() => [])
        : [];
    } finally {
      setReading(false);
    }
    if (records.length === 0) {
      await end([]);
    } else {
      setAsked(records);
    }
  };
  const count = asked?.length ?? 0;
  return (
    <>
      <Button
        disabled={signOut.isPending || reading}
        onClick={() => void click()}
      >
        {children}
      </Button>
      <AlertDialog.Root
        open={asked !== undefined}
        onOpenChange={(open) => {
          if (!open && !signOut.isPending) {
            setAsked(undefined);
          }
        }}
      >
        <ConfirmFrame
          title={t("sign_out.pending.title", { count })}
          description={t("sign_out.pending.body", { count })}
        >
          <div className="flex flex-wrap gap-2">
            <Button
              onClick={() => {
                const now = new Date().toISOString();
                saveJson(
                  exportFileName(now),
                  pendingExport(userId, exportedOfRecords(asked ?? []), now),
                );
              }}
            >
              {t("sign_out.pending.export")}
            </Button>
          </div>
          <ConfirmButtons
            cancel={t("sign_out.pending.cancel")}
            confirm={t("sign_out.pending.confirm")}
            pending={signOut.isPending}
            onConfirm={() => void end(asked ?? [])}
          />
        </ConfirmFrame>
      </AlertDialog.Root>
    </>
  );
}
