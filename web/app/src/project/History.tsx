// The undo and redo of the user's own changes of a project's content (PRD
// FR-EDIT-08), which a project's pages show while the user may change it:
// each sends the step back to the server as a batch of its own, which the
// server refuses whole, with nothing changed, where it would overwrite what
// changed since (AC4), and then says why. The canvas's own controls and
// shortcuts come later (plan items 61 and 65).

import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { useDocumentState, useProjectDocument } from "../document/react";
import { Button } from "../ui/Button";
import { Alert, Status } from "../ui/Page";

export function History({ projectId }: { projectId: string }) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const undoable = useDocumentState(doc, (state) => state.undoable);
  const redoable = useDocumentState(doc, (state) => state.redoable);
  const stepping = useDocumentState(doc, (state) => state.stepping);
  const [done, setDone] = useState<"undo" | "redo">();
  const step = useMutation({
    mutationFn: (which: "undo" | "redo") =>
      which === "undo" ? doc.undo() : doc.redo(),
    onMutate: () => setDone(undefined),
    onSuccess: (_result, which) => setDone(which),
  });
  const reason = refusalOf(step.error, t)?.message ?? "";
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
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
      </div>
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
    </div>
  );
}
