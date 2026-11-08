// The undo and redo of the user's own changes of a project's content (PRD
// FR-EDIT-08), which a project's pages show while the user may change it:
// each sends the step back to the server as a batch of its own, which the
// server refuses whole, with nothing changed, where it would overwrite what
// changed since (AC4), and then says why. A board's page shows them in its
// canvas's toolbar (plan item 61), which says so of itself (staticData), and
// the project's layout shows them above every other page; the shortcuts come
// later (plan item 65).

import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { useDocumentState, useProjectDocument } from "../document/react";
import { Button } from "../ui/Button";
import { Alert, Status } from "../ui/Page";

/** The undo and redo of a project's page, which its controls and what they say share (useHistory). */
export type HistoryOf = ReturnType<typeof useHistory>;

/**
 * Returns the undo and redo of the project of projectId: how many steps
 * each has, whether one is under way, what became of the last, and what
 * sends one, which the toolbar's buttons and the canvas's undo of a bulk
 * move share, so that each says what became of either.
 */
export function useHistory(projectId: string) {
  const doc = useProjectDocument(projectId);
  const undoable = useDocumentState(doc, (state) => state.undoable);
  const redoable = useDocumentState(doc, (state) => state.redoable);
  const stepping = useDocumentState(doc, (state) => state.stepping);
  const [done, setDone] = useState<"undo" | "redo">();
  // A step that there was none of once it was sent, as a redo that a change
  // saved just before ended, says nothing.
  const step = useMutation({
    mutationFn: (which: "undo" | "redo") =>
      which === "undo" ? doc.undo() : doc.redo(),
    onMutate: () => setDone(undefined),
    onSuccess: (stepped, which) => setDone(stepped ? which : undefined),
  });
  return { undoable, redoable, stepping, done, step };
}

/** The buttons of the undo and the redo. */
export function HistoryButtons({ history }: { history: HistoryOf }) {
  const { t } = useTranslation();
  const { undoable, redoable, stepping, step } = history;
  return (
    <>
      <Button
        disabled={undoable === 0 || stepping}
        onClick={() => step.mutate("undo")}
      >
        {t("history.undo")}
      </Button>
      <Button
        disabled={redoable === 0 || stepping}
        onClick={() => step.mutate("redo")}
      >
        {t("history.redo")}
      </Button>
    </>
  );
}

/** What became of the last undo or redo, as a status, or why it was refused. */
export function HistoryStatus({ history }: { history: HistoryOf }) {
  const { t } = useTranslation();
  const { done, step } = history;
  const reason = refusalOf(step.error, t)?.message ?? "";
  return (
    <>
      <Status
        message={
          done === "undo"
            ? t("history.undone")
            : done === "redo"
              ? t("history.redone")
              : undefined
        }
      />
      <Alert
        message={
          step.error === null
            ? undefined
            : step.variables === "undo"
              ? t("history.undo_failed", { reason })
              : t("history.redo_failed", { reason })
        }
      />
    </>
  );
}

/** The undo and the redo, with what became of them, as the project's layout shows them. */
export function History({ projectId }: { projectId: string }) {
  const history = useHistory(projectId);
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <HistoryButtons history={history} />
      </div>
      <HistoryStatus history={history} />
    </div>
  );
}
