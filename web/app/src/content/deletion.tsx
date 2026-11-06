// The deletion of a variable or a component to the project's trash, guarded
// (PRD FR-VAR-04, FR-COMP-04): one that sites of the project use is refused,
// with how many use it, how many of each use, and the first hundred of them,
// as the project stands when the deletion applies (variable_in_use,
// component_in_use), which the confirmation then lists, and offers to delete
// anyway only to a role that may publish the project's snapshots, as
// publish_min_role says (§5.6), which leaves the expressions that used it
// naming what no longer resolves, for validation to report.

import { ApiError, type ProjectBody } from "@storycreep/shared/api";
import { AlertDialog } from "radix-ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { useExpected } from "../project/dialogs";
import { useCommands } from "../project/queries";
import { Button } from "../ui/Button";
import {
  ConfirmButtons,
  ConfirmFrame,
  closesBy,
  focusBackTo,
  type Opener,
} from "../ui/Dialog";
import { Alert } from "../ui/Page";
import { useDeleting } from "../ui/Panel";
import { type FoundUsage, UsageItem, usageKey } from "./usages";

/** How the roles of a project rank, the least first, as the permission matrix orders them (§5.6). */
const roleRank: Readonly<Record<ProjectBody["role"], number>> = {
  viewer: 0,
  commenter: 1,
  editor: 2,
  admin: 3,
  owner: 4,
};

/** Reports whether the user's role on project may publish its snapshots, and so force a deletion (FR-VAR-04). */
export function publishes(project: ProjectBody): boolean {
  return roleRank[project.role] >= roleRank[project.publish_min_role];
}

/** What a refusal of a used variable's or component's deletion says of its usages. */
interface InUse {
  count: number;
  counts: Partial<Record<FoundUsage["use"], number>>;
  usages: FoundUsage[];
}

/** Returns what error, a refusal of a deletion, says of the usages, or undefined if it is no refusal for them. */
function inUseOf(error: unknown): InUse | undefined {
  if (
    !(error instanceof ApiError) ||
    (error.error?.code !== "variable_in_use" &&
      error.error?.code !== "component_in_use")
  ) {
    return undefined;
  }
  const details = (error.error.details ?? {}) as {
    usage_count?: unknown;
    counts?: unknown;
    usages?: unknown;
  };
  return {
    count: typeof details.usage_count === "number" ? details.usage_count : 0,
    counts:
      typeof details.counts === "object" && details.counts !== null
        ? (details.counts as InUse["counts"])
        : {},
    usages: Array.isArray(details.usages)
      ? (details.usages as FoundUsage[])
      : [],
  };
}

/** What the deletion deletes, with the revision that the page shows. */
export interface Deleted {
  kind: "variable" | "component";
  id: string;
  /** What the page names it, as a variable's key or a component's name. */
  name: string;
  rev: number;
}

/**
 * The confirmation that deletes a variable or a component to the trash,
 * expecting the revision that it opened with, which lists the usages that
 * a refusal names, and then offers to delete it anyway, if the user's role
 * may, or else says why it was not deleted.
 */
export function DeleteContentDialog({
  project,
  deleted,
  opener,
  onClose,
  onDeleting,
  onDeleted,
}: {
  project: ProjectBody;
  deleted: Deleted;
  opener: Opener;
  onClose: () => void;
  /** Told true as the confirmation opens, and false once it has gone, however it goes (useDeleting). */
  onDeleting: (deleting: boolean) => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation();
  useDeleting(onDeleting);
  const variable = deleted.kind === "variable";
  const commands = useCommands(project.id);
  const rev = useExpected(deleted.rev, commands.error);
  // The usages that the last refusal listed, which the confirmation shows
  // while it is open, as a forced deletion is under way, or refused too.
  const [inUse, setInUse] = useState<InUse>();
  const mayForce = publishes(project);
  const remove = (force: boolean) =>
    commands.mutate(
      [
        variable
          ? {
              type: "variable.delete",
              payload: { id: deleted.id, ...(force ? { force } : {}) },
              expected_rev: { [deleted.id]: rev },
            }
          : {
              type: "component.delete",
              payload: { id: deleted.id, ...(force ? { force } : {}) },
              expected_rev: { [deleted.id]: rev },
            },
      ],
      {
        onSuccess: onDeleted,
        onError: (error) => {
          const found = inUseOf(error);
          if (found !== undefined) {
            setInUse(found);
          }
        },
      },
    );
  const refusal =
    inUseOf(commands.error) === undefined
      ? refusalOf(commands.error, t)
      : undefined;
  const listed = inUse?.usages.length ?? 0;
  return (
    <AlertDialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <ConfirmFrame
        title={t("deletion.title", { name: deleted.name })}
        description={
          variable ? t("deletion.body_variable") : t("deletion.body_component")
        }
        onCloseAutoFocus={focusBackTo(opener)}
      >
        <Alert message={refusal?.message} />
        {/* The live region is there before its message, so that a screen
            reader reads the refusal as it comes. */}
        <div role="status">
          {inUse !== undefined && (
            <p>
              {variable
                ? t("deletion.in_use_variable", {
                    count: inUse.count,
                    reads: inUse.counts.read ?? 0,
                    writes: inUse.counts.write ?? 0,
                  })
                : t("deletion.in_use_component", {
                    count: inUse.count,
                    speaker: inUse.counts.speaker ?? 0,
                    mention: inUse.counts.mention ?? 0,
                    reads: inUse.counts.read ?? 0,
                    writes: inUse.counts.write ?? 0,
                  })}
            </p>
          )}
        </div>
        {inUse !== undefined && (
          <div className="space-y-2">
            <ul
              aria-label={t("deletion.usages", { name: deleted.name })}
              className="max-h-64 list-disc space-y-1 overflow-y-auto ps-5"
            >
              {inUse.usages.map((usage) => (
                <UsageItem
                  key={`${usage.use}:${usageKey(usage)}`}
                  projectId={project.id}
                  usage={usage}
                  use={usage.use}
                />
              ))}
            </ul>
            {inUse.count > listed && (
              <p className="text-fg-muted">
                {t("deletion.more", { shown: listed })}
              </p>
            )}
            <p>
              {mayForce
                ? variable
                  ? t("deletion.force_variable")
                  : t("deletion.force_component")
                : t("deletion.no_force")}
            </p>
          </div>
        )}
        {inUse === undefined ? (
          <ConfirmButtons
            cancel={t("boards.cancel")}
            confirm={t("deletion.confirm")}
            pending={commands.isPending}
            onConfirm={() => remove(false)}
          />
        ) : mayForce ? (
          <ConfirmButtons
            cancel={t("boards.cancel")}
            confirm={t("deletion.confirm_anyway")}
            pending={commands.isPending}
            onConfirm={() => remove(true)}
          />
        ) : (
          <div className="flex flex-wrap justify-end gap-2">
            <AlertDialog.Cancel asChild>
              <Button>{t("deletion.close")}</Button>
            </AlertDialog.Cancel>
          </div>
        )}
      </ConfirmFrame>
    </AlertDialog.Root>
  );
}
