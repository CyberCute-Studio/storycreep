// What a panel's form of an entity of a project's content does as it saves,
// as a variable's, a component's, or a component type's: it sends what of
// the entity changed in one batch, each command expecting the revision that
// the form shows, and shows the entity as the page has it until the user is
// in the form, and then as it was (useHeld), as the board's form does. A
// save refused for a change made elsewhere of another field is sent again by
// the document (PRD §7.10); one of a field that the form changes too names
// each such field with both values (ConflictError), and shows the entity as
// it is now, but for what the user changed, which it keeps, so that saving
// again keeps it, over the entity as it is now; using theirs shows the
// entity as it is now.

import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { Doc } from "../document/doc";
import { useProjectDocument } from "../document/react";
import { ConflictError, type Overlap } from "../document/store";
import { type Command, conflictOf, useCommands } from "../project/queries";
import { Button } from "../ui/Button";
import { useHeld } from "../ui/held";
import { Status } from "../ui/Page";

/**
 * Returns the form of entity, as the document of projectId has it, which
 * current reads of a version of the document, or undefined once it has
 * none to show, as once it is gone.
 */
export function useEntityForm<T extends { id: string; rev: number }>(
  projectId: string,
  entity: T,
  current: (view: Doc, id: string) => T | undefined,
) {
  const doc = useProjectDocument(projectId);
  const commands = useCommands(projectId);
  const [unchanged, setUnchanged] = useState(false);
  const held = useHeld(entity);
  // The entity as the server had it when it refused the form's save for a
  // change of what the form changes too, which saving again changes over.
  const [theirs, setTheirs] = useState<T>();
  const against = theirs ?? held.shown;
  const overlaps =
    commands.error instanceof ConflictError
      ? commands.error.overlaps.flatMap((o) => o.fields)
      : [];
  return {
    commands,
    held,
    shown: held.shown,
    /** The entity that the form's changes are made against: as it showed it, or as the server had it once it refused them. */
    against,
    unchanged,
    overlaps,
    /** Forgets what the form's last save said, as once the form refused a save itself. */
    refused: () => {
      commands.reset();
      setUnchanged(false);
    },
    /**
     * Saves changes, the commands of what the user changed over against,
     * which typed holds, as the entity's fields: none says that nothing
     * changed, and holds the entity as it is now while the focus is in the
     * form.
     */
    save: (changes: Command[], typed: Partial<T>) => {
      setUnchanged(changes.length === 0);
      if (changes.length === 0) {
        commands.reset();
        held.holdNow();
        setTheirs(undefined);
        return;
      }
      commands.mutate(changes, {
        onSuccess: () => {
          held.release();
          setTheirs(undefined);
        },
        onError: (error) => {
          if (!conflictOf(error)) {
            return;
          }
          // The entity as the server has it now: as the refusal gave it,
          // where it named a field that the form changes too, or else as
          // the document took it from the refusal; none once it is gone.
          const state =
            error instanceof ConflictError
              ? error.overlaps.find((o) => o.id === against.id && !o.gone)
                  ?.state
              : undefined;
          const now =
            (state as unknown as T | undefined) ??
            current(doc.store.getState().view, against.id);
          if (now === undefined) {
            held.release();
            setTheirs(undefined);
            return;
          }
          setTheirs(now);
          held.hold({ ...now, ...typed });
        },
      });
    },
    /** Shows the entity as it is now, without what the user changed. */
    useTheirs: () => {
      commands.reset();
      held.release();
      setTheirs(undefined);
    },
  };
}

/** What a form of an entity says of its last save: that nothing changed, or that it saved. */
export function SaveStatus({
  form,
}: {
  form: { unchanged: boolean; commands: { isSuccess: boolean } };
}) {
  const { t } = useTranslation();
  return (
    <Status
      message={
        form.unchanged
          ? t("board.unchanged")
          : form.commands.isSuccess
            ? t("board.saved")
            : undefined
      }
    />
  );
}

/**
 * What a form of an entity says of a save refused for a change made
 * elsewhere of what it changes too: each such field, with the value of
 * each, which fieldName and shownValue name and show, and a button that
 * shows the entity as it is now.
 */
export function ConflictNotice({
  form,
  fieldName,
  shownValue,
}: {
  form: { overlaps: Overlap["fields"]; useTheirs: () => void };
  fieldName: (field: string) => string;
  /** Returns what the notice shows of a value, or undefined for one that does not read as a line of text, as a description. */
  shownValue: (field: string, value: unknown) => string | undefined;
}) {
  const { t } = useTranslation();
  if (form.overlaps.length === 0) {
    return null;
  }
  return (
    <div className="space-y-2 rounded-md border border-danger px-4 py-3">
      <p>{t("conflict.intro")}</p>
      <ul className="list-disc ps-5">
        {form.overlaps.map((o) => {
          const theirs = shownValue(o.field, o.theirs);
          const mine = shownValue(o.field, o.mine);
          return (
            <li key={o.field} className="break-words">
              {theirs === undefined || mine === undefined
                ? t("conflict.field_changed", { field: fieldName(o.field) })
                : t("conflict.field", {
                    field: fieldName(o.field),
                    theirs,
                    mine,
                  })}
            </li>
          );
        })}
      </ul>
      <Button onClick={form.useTheirs}>{t("conflict.use_theirs")}</Button>
    </div>
  );
}
