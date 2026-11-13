// The undo and redo of the user's own changes of a project's content (PRD
// FR-EDIT-08), which a project's pages show while the user may change it:
// each sends the step back to the server as a batch of its own, which the
// server refuses whole, with nothing changed, where it would overwrite what
// changed since (AC4), and then says why. A board's canvas shows them in its
// toolbar (plan item 61), which says so to the project's layout while it is
// shown (useOwnHistory), and the layout shows them above the page whenever
// none does, as above a board's page that shows no canvas, as once the
// board went. Whichever shows them registers the commands of the undo and
// the redo, whose keys, Ctrl+Z and Ctrl+Y, or Cmd+Z and Cmd+Shift+Z on
// macOS, act outside the fields that the user types in, so that what they
// say is what the buttons would (historyHandlers, plan note N72).

import { useMutation } from "@tanstack/react-query";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useLayoutEffect,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { type Handlers, useCommandHandlers } from "../commands/registry";
import { useDocumentState, useProjectDocument } from "../document/react";
import { Button } from "../ui/Button";
import { Alert, Status } from "../ui/Page";

/** The undo and redo of a project's page, which its controls and what they say share (useHistory). */
export type HistoryOf = ReturnType<typeof useHistory>;

/** Tells the project's layout of a part of its page that shows the undo and the redo, as it comes (1) and goes (-1). */
const HistoryOwners = createContext<((by: 1 | -1) => void) | undefined>(
  undefined,
);

/** Whether a part of the project's page shows the undo and the redo. */
const HistoryOwnedNow = createContext(false);

/**
 * Says that the part of the page that calls it shows the undo and the redo
 * itself, while owns is true and it is shown, so that the project's layout
 * does not show them above the page too.
 */
export function useOwnHistory(owns: boolean): void {
  const own = useContext(HistoryOwners);
  useLayoutEffect(() => {
    if (!owns || own === undefined) {
      return;
    }
    own(1);
    return () => own(-1);
  }, [own, owns]);
}

/**
 * What the project's layout holds its pages in, which counts the parts of
 * them that show the undo and the redo (useOwnHistory), so that its own,
 * above them (LayoutHistory), are shown while none does.
 */
export function HistoryOwned({ children }: { children: ReactNode }) {
  const [owners, setOwners] = useState(0);
  const own = useCallback((by: 1 | -1) => setOwners((n) => n + by), []);
  return (
    <HistoryOwners.Provider value={own}>
      <HistoryOwnedNow.Provider value={owners > 0}>
        {children}
      </HistoryOwnedNow.Provider>
    </HistoryOwners.Provider>
  );
}

/** The undo and the redo above a project's page, unless a part of the page shows them (HistoryOwned). */
export function LayoutHistory({ projectId }: { projectId: string }) {
  const owned = useContext(HistoryOwnedNow);
  return owned ? null : <History projectId={projectId} />;
}

/**
 * Returns the undo and redo of the project of projectId: how many steps
 * each has, and how often they changed (version), whether one is under
 * way, what became of the last, and what
 * sends one, which the toolbar's buttons and the canvas's undo of a bulk
 * move share, so that each says what became of either.
 */
export function useHistory(projectId: string) {
  const doc = useProjectDocument(projectId);
  const undoable = useDocumentState(doc, (state) => state.undoable);
  const redoable = useDocumentState(doc, (state) => state.redoable);
  const version = useDocumentState(doc, (state) => state.historyVersion);
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
  return { undoable, redoable, version, stepping, done, step };
}

/**
 * Reports whether history can step which way now: it has a step to undo,
 * or to redo, and none is under way, as its buttons, its keys, and the
 * palette's entries say alike.
 */
function canStep(history: HistoryOf, which: "undo" | "redo"): boolean {
  return (
    (which === "undo" ? history.undoable : history.redoable) > 0 &&
    !history.stepping
  );
}

/**
 * Returns the commands of the undo and the redo, of history, which the
 * buttons that a part of the page shows send too, so that a key and the
 * palette say what became of them where the buttons do: as the canvas
 * registers them among its own, which run only outside a drag
 * (BoardCanvas's whileDragging), and the project's layout above its pages.
 */
export function historyHandlers(history: HistoryOf): Handlers {
  return {
    "history.undo": {
      enabled: canStep(history, "undo"),
      run: () => history.step.mutate("undo"),
    },
    "history.redo": {
      enabled: canStep(history, "redo"),
      run: () => history.step.mutate("redo"),
    },
  };
}

/** The buttons of the undo and the redo. */
export function HistoryButtons({ history }: { history: HistoryOf }) {
  const { t } = useTranslation();
  const { step } = history;
  return (
    <>
      <Button
        disabled={!canStep(history, "undo")}
        onClick={() => step.mutate("undo")}
      >
        {t("history.undo")}
      </Button>
      <Button
        disabled={!canStep(history, "redo")}
        onClick={() => step.mutate("redo")}
      >
        {t("history.redo")}
      </Button>
    </>
  );
}

/** What became of the last undo or redo, as a status, or why it was refused. */
export function HistoryStatus({
  history,
  alertClassName,
}: {
  history: HistoryOf;
  /** The classes of why a step was refused, as Alert's, as of one over the canvas, which has a background of its own. */
  alertClassName?: string;
}) {
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
        className={alertClassName}
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
  useCommandHandlers("project", historyHandlers(history));
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <HistoryButtons history={history} />
      </div>
      <HistoryStatus history={history} />
    </div>
  );
}
