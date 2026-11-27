// Signing out of a page whose device keeps changes that the server has yet
// to answer (PRD FR-OFF-03, plan note N98): the device keeps them for the
// account, across a session that ended by itself and another account's
// signing in, until they are sent; signing out asks first, naming how many
// there are, of every project of the account's, and offers them as JSON,
// for support, before it takes them off the device with the account's
// copies, once the server has signed the page out, which the browser's
// storage remembers from when the user signs out anyway, so that a tab that
// closes first, as while the server answers, leaves the removal to the next
// page that takes the account's changes, rather than the changes to be sent
// (QueueStore.intend). Signing out of a device that keeps none asks
// nothing. The device's database is read as the user clicks, once it has
// opened, as on a page that has just loaded, and once the page's documents
// have written the changes that they were keeping, and again as the user
// signs out anyway, which asks anew where the device keeps changes since
// that the dialog did not name. A database that does not open in time, as
// one that another tab blocks, or that cannot be read, may keep some all
// the same: signing out asks first then too, saying that it could not read
// them, and, signed out anyway, takes any off the device once it can.

import type { SignedInBody } from "@storycreep/shared/api";
import { useQueryClient } from "@tanstack/react-query";
import { AlertDialog } from "radix-ui";
import { type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useDocuments } from "../document/react";
import { Button, type ButtonProps } from "../ui/Button";
import { ConfirmButtons, ConfirmFrame } from "../ui/Dialog";
import { Alert } from "../ui/Page";
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
 * What signs the page out of a session, as the mutation of useSignOut
 * (auth/signOut.ts), or the revoking of the page's own session on the
 * sessions page, which resolves once the page is signed out, and rejects
 * where it is not, which its page says.
 */
export interface SignsOut {
  mutateAsync: (session: SignedInBody) => Promise<unknown>;
  isPending: boolean;
}

/**
 * The button that signs session's page out by signOut, which asks first
 * where the device keeps changes of the account's that the server has yet
 * to answer, and then takes them off the device too; children are its
 * label, and variant its look.
 */
export function SignOutButton({
  signOut,
  session,
  variant,
  children,
}: {
  signOut: SignsOut;
  session: SignedInBody;
  variant?: ButtonProps["variant"];
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const keeper = useKeeper();
  const documents = useDocuments();
  const client = useQueryClient();
  // The account's changes that the device keeps, once the click has found
  // some, or unread, where it could not read them, while the dialog that
  // asks is open.
  const [asked, setAsked] = useState<QueueRecord[] | "unread">();
  const [reading, setReading] = useState(false);
  // Why signing out anyway failed, which the dialog says, as the page's
  // own message of it is behind the dialog (signOutFailure).
  const [failure, setFailure] = useState<unknown>();
  const userId = session.user_id;
  // The device's database, unless the page knows that it keeps nothing, as
  // over plain HTTP, or once it failed to open, where the page keeps no
  // queue: one still opening is read once it has (opensWithin), and so is
  // one that the keeper could not open at first, which the queue's
  // documents open again as they use it (OfflineDatabase.open), and may
  // have written changes to since.
  const dbNow = () =>
    keeper !== undefined &&
    (documents.keepsQueue || keeper.store.getState().available !== false)
      ? keeper.db
      : undefined;
  /**
   * Resolves to the account's changes that the device keeps, none where it
   * keeps none, or undefined where it cannot read them, as of a database
   * that does not open within openWait.
   */
  const read = async (): Promise<QueueRecord[] | undefined> => {
    const db = dbNow();
    if (db === undefined) {
      return [];
    }
    setReading(true);
    try {
      // A change that a document was still keeping as the user clicked, as
      // one made just before, is on the device once its write is done.
      await documents.whenWritten(openWait);
      return (await opensWithin(db, openWait))
        ? await db.queued(userId).catch(() => undefined)
        : undefined;
    } finally {
      setReading(false);
    }
  };
  // Signs out, and takes off the device the changes that the user signed
  // out anyway with, those named, by their projects and IDs, or, of a
  // device that could not be read, any (undefined): one that a document
  // kept as the page signed out, which the dialog did not name, stays, the
  // account's, as one kept after. The user's decision is remembered before
  // the page signs out, and forgotten where the page is still signed in,
  // which keeps them (intendDiscard).
  const end = async (records: readonly QueueRecord[] | undefined) => {
    setFailure(undefined);
    const forget =
      records === undefined || records.length > 0
        ? documents.intendDiscard(userId, records)
        : undefined;
    try {
      await signOut.mutateAsync(session);
    } catch (error) {
      // A page that the server signed out, whose showing itself anew then
      // failed (endSession), is signed out all the same, and the changes go.
      if (client.getQueryData(sessionQuery.queryKey) !== null) {
        forget?.();
        // The page says why it is still signed in, as the header does
        // (signOutFailure), and so does the dialog, where it is open.
        setFailure(error);
        return;
      }
    }
    setAsked(undefined);
    if (records === undefined || records.length > 0) {
      // The user decided: the changes go with the account's copies, and no
      // document that held them writes one there again; those that the
      // device refuses to take off it now it takes once it can, before a
      // document takes the account's changes again (QueueStore.discard).
      await (
        documents.discardQueued(userId, records) ??
        dbNow()?.deleteQueued(userId, records)
      )?.catch(() => undefined);
    }
  };
  const click = async () => {
    setFailure(undefined);
    const records = await read();
    if (records?.length === 0) {
      await end([]);
    } else {
      setAsked(records ?? "unread");
    }
  };
  // Signing out anyway takes off the device the changes that the dialog
  // named, or, where it could not read them, those that it reads now,
  // which are none where it finds none, as a change kept as the page signs
  // out stays, or any, where it still cannot, and asks anew where it keeps
  // one that it did not name, as one that it reads now.
  const confirm = async (named: readonly QueueRecord[] | undefined) => {
    const records = await read();
    const known = new Set((named ?? []).map((r) => `${r.project_id}/${r.id}`));
    if (records?.some((r) => !known.has(`${r.project_id}/${r.id}`))) {
      setAsked(records);
      return;
    }
    await end(named ?? records);
  };
  const named = Array.isArray(asked) ? asked : undefined;
  const count = named?.length ?? 0;
  return (
    <>
      <Button
        variant={variant}
        disabled={signOut.isPending || reading}
        onClick={() => void click()}
      >
        {children}
      </Button>
      <AlertDialog.Root
        open={asked !== undefined}
        onOpenChange={(open) => {
          // Not while signing out anyway reads the device again, which signs
          // out next, as the buttons say (pending).
          if (!open && !signOut.isPending && !reading) {
            setAsked(undefined);
            setFailure(undefined);
          }
        }}
      >
        <ConfirmFrame
          title={
            named === undefined
              ? t("sign_out.unread.title")
              : t("sign_out.pending.title", { count })
          }
          description={
            named === undefined
              ? t("sign_out.unread.body")
              : t("sign_out.pending.body", { count })
          }
        >
          {named !== undefined && (
            <div className="flex flex-wrap gap-2">
              <Button
                onClick={() => {
                  const now = new Date().toISOString();
                  saveJson(
                    exportFileName(now),
                    pendingExport(userId, exportedOfRecords(named), now),
                  );
                }}
              >
                {t("sign_out.pending.export")}
              </Button>
            </div>
          )}
          <Alert
            message={
              failure === undefined
                ? undefined
                : t("auth.error.sign_out", {
                    reason: refusalOf(failure, t)?.message ?? "",
                  })
            }
          />
          <ConfirmButtons
            cancel={t("sign_out.pending.cancel")}
            confirm={t("sign_out.pending.confirm")}
            pending={signOut.isPending || reading}
            onConfirm={() => void confirm(named)}
          />
        </ConfirmFrame>
      </AlertDialog.Root>
    </>
  );
}
