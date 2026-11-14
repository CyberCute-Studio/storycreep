// Play mode's panel (PRD FR-PLAY-01, §5.8.7, plan note N78), beside a
// board's canvas, or a sheet over it below 900 px (ui/Panel.tsx): where the
// flow is, its speaker, its text, and its choices, which a press takes, why
// it ended, its runtime errors, which strict mode halts it at, with where
// each is and its expression's source, and what the projection stood in
// for. Closing it stops play.

import { Link } from "@tanstack/react-router";
import type { ReactNode, RefObject } from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "zustand";
import { titleOf } from "../canvas/labels";
import { useDocumentState } from "../document/react";
import type { ProjectDocument } from "../document/store";
import { Button } from "../ui/Button";
import { Alert, textLink } from "../ui/Page";
import { Panel } from "../ui/Panel";
import {
  endedMessage,
  errorMessage,
  fieldName,
  standInMessage,
} from "./messages";
import type { PlayError, PlaySession } from "./session";

export function PlayPanel({
  projectId,
  doc,
  session,
  returnFocus,
  headingRef,
}: {
  projectId: string;
  doc: ProjectDocument;
  session: PlaySession;
  /** What gets the focus once the panel has gone, as the canvas. */
  returnFocus: () => HTMLElement | null;
  headingRef?: RefObject<HTMLHeadingElement | null>;
}) {
  const { t } = useTranslation();
  const state = useStore(session.store);
  const nodes = useDocumentState(doc, (s) => s.view.nodes);
  const boards = useDocumentState(doc, (s) => s.view.boards);
  if (state.status === "idle") {
    return null;
  }
  // Where an entity is, by its node's title and its board's name, as the
  // page has them, linked to the node on its board's canvas, as a finding
  // of the validation is (FR-DEP-03 AC3).
  const where = (board: string | undefined, node: string | undefined) => {
    const b = board === undefined ? undefined : boards.get(board);
    const n = node === undefined ? undefined : nodes.get(node);
    const boardName = b?.name ?? board ?? "";
    if (n === undefined) {
      return boardName;
    }
    return t("play.where", { node: titleOf(n, t), board: boardName });
  };
  const link = (
    board: string | undefined,
    node: string | undefined,
    text: string,
  ) =>
    board === undefined ? (
      <span>{text}</span>
    ) : (
      <Link
        to="/projects/$projectId/boards/$boardId"
        params={{ projectId, boardId: board }}
        search={node === undefined ? {} : { node }}
        className={textLink}
      >
        {text}
      </Link>
    );
  return (
    <Panel
      title={t("play.title")}
      onClose={() => session.stop()}
      returnFocus={returnFocus}
      headingRef={headingRef}
    >
      {state.status === "loading" ? (
        <p role="status">
          {state.total === 0
            ? t("play.reading")
            : t("play.reading_count", {
                done: state.done,
                total: state.total,
              })}
        </p>
      ) : state.status === "failed" ? (
        <div className="space-y-2">
          <Alert
            message={state.reading ? t("play.failed") : t("play.unplayable")}
          />
          {!state.reading && state.error instanceof Error && (
            <code className="block break-words whitespace-pre-wrap text-sm">
              {state.error.message}
            </code>
          )}
          <Button onClick={() => void session.restart()}>
            {t("play.try_again")}
          </Button>
        </div>
      ) : (
        <PlayFlowView
          state={state}
          session={session}
          where={where}
          link={link}
        />
      )}
      <div className="flex flex-wrap gap-2">
        {state.status === "playing" && (
          <Button onClick={() => void session.restart()}>
            {t("play.restart")}
          </Button>
        )}
        <Button onClick={() => session.stop()}>{t("play.stop")}</Button>
      </div>
    </Panel>
  );
}

function PlayFlowView({
  state,
  session,
  where,
  link,
}: {
  state: Extract<PlaySession["state"], { status: "playing" }>;
  session: PlaySession;
  where: (board: string | undefined, node: string | undefined) => string;
  link: (
    board: string | undefined,
    node: string | undefined,
    text: string,
  ) => ReactNode;
}) {
  const { t } = useTranslation();
  const { flow, projection } = state;
  const standIns = projection.standIns;
  return (
    <div className="space-y-4">
      {state.changed && (
        <div
          role="status"
          className="space-y-2 rounded-md border border-border p-3"
        >
          <p>{t("play.changed")}</p>
          <Button onClick={() => void session.restart()}>
            {t("play.restart")}
          </Button>
        </div>
      )}
      {/* Where the flow is, said as it changes, whole each time. */}
      <section
        aria-label={t("play.current")}
        aria-live="polite"
        aria-atomic="true"
        className="space-y-2"
      >
        {flow.node === "" ? (
          <p className="text-fg-muted">{t("play.nowhere")}</p>
        ) : (
          <>
            <p className="text-sm text-fg-muted">
              {where(flow.board, undefined)}
            </p>
            <h3 className="font-semibold break-words">
              {flow.title === "" ? t("canvas.node.untitled") : flow.title}
            </h3>
            {flow.speaker !== "" && (
              <p className="font-medium break-words">
                {t("play.speaker", { speaker: flow.speaker })}
              </p>
            )}
            <p className="break-words whitespace-pre-line">{flow.text}</p>
          </>
        )}
        {flow.ended !== undefined && (
          <p className="font-medium">{endedMessage(t, flow.ended)}</p>
        )}
      </section>
      {flow.phase === "awaiting_choice" && flow.choices.length > 0 && (
        <section aria-label={t("play.choices")}>
          <ul className="space-y-2">
            {flow.choices.map((c) => (
              <li key={c.id}>
                <Button
                  disabled={flow.halted}
                  onClick={() => session.choose(c.id)}
                  className="w-full justify-start text-left whitespace-pre-line"
                >
                  {c.text === "" ? t("play.continue") : c.text}
                </Button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {flow.halted && <Alert message={t("play.halted")} />}
      {flow.errors.length > 0 && (
        <section aria-labelledby="play-errors" className="space-y-2">
          <h3 id="play-errors" className="font-semibold">
            {t("play.errors", { count: flow.errors.length })}
          </h3>
          <ol className="space-y-2">
            {flow.errors.map((e, i) => (
              <ErrorItem
                // The errors only grow, in order.
                // biome-ignore lint/suspicious/noArrayIndexKey: the list is appended to alone
                key={i}
                error={e}
                where={where}
                link={link}
              />
            ))}
          </ol>
        </section>
      )}
      {standIns.length > 0 && (
        <details className="space-y-2">
          <summary className="min-h-11 cursor-pointer py-3">
            {t("play.stand_ins", { count: standIns.length })}
          </summary>
          <ul className="list-disc space-y-1 ps-5">
            {standIns.map((s, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: the list is the projection's, which does not change
              <li key={i}>
                {link(
                  s.board,
                  s.node,
                  standInMessage(t, s, where(s.board, s.node)),
                )}
                {s.kind === "expression" && s.source !== "" && (
                  <code className="mt-1 block break-words whitespace-pre-wrap">
                    {s.source}
                  </code>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

function ErrorItem({
  error,
  where,
  link,
}: {
  error: PlayError;
  where: (board: string | undefined, node: string | undefined) => string;
  link: (
    board: string | undefined,
    node: string | undefined,
    text: string,
  ) => ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <li className="space-y-1 rounded-md border border-danger p-2">
      <p>
        {errorMessage(t, error.code)}{" "}
        <code className="text-sm text-fg-muted">{error.code}</code>
      </p>
      {error.board !== undefined && (
        <p className="text-sm">
          {link(
            error.board,
            error.node,
            error.expression === ""
              ? where(error.board, error.node)
              : t("play.where_field", {
                  where: where(error.board, error.node),
                  field: fieldName(t, error.expression),
                }),
          )}
        </p>
      )}
      {error.source !== undefined && (
        <code className="block break-words whitespace-pre-wrap text-sm">
          {error.source}
        </code>
      )}
    </li>
  );
}
