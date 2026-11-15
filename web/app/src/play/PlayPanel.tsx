// Play mode's panel (PRD FR-PLAY-01, §5.8.7, plan note N78), beside a
// board's canvas, or a sheet over it below 900 px (ui/Panel.tsx): where the
// flow is, its speaker, its text, and its choices, which a press takes, and
// those that it hides, with why (FR-PLAY-03), Step back and Reset
// (FR-PLAY-04), why it ended, its runtime errors, which strict mode halts it
// at, with where each is and its expression's source, the values of its
// state, which the author sets by hand (FR-PLAY-02), its history, which
// copies as text (FR-PLAY-04), and what the projection stood in for (plan
// note N79). Closing it stops play.

import { ApiError, NetworkError } from "@storycreep/shared/api";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import {
  type ReactNode,
  type RefObject,
  useEffect,
  useId,
  useMemo,
  useRef,
} from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "zustand";
import { useShallow } from "zustand/react/shallow";
import { refusalOf } from "../auth/refusal";
import { titleOf } from "../canvas/labels";
import { useDocumentState } from "../document/react";
import type { ProjectDocument } from "../document/store";
import { focusIsNowhere } from "../shell/focus";
import { Button } from "../ui/Button";
import { Alert, textLink } from "../ui/Page";
import { Panel } from "../ui/Panel";
import { HiddenChoices } from "./HiddenChoices";
import { History } from "./History";
import { Inspector } from "./Inspector";
import {
  endedMessage,
  errorMessage,
  expressionPlace,
  standInMessage,
} from "./messages";
import {
  followsFlow,
  type PlayError,
  type PlaySession,
  type PlayState,
} from "./session";
import type { Where } from "./steps";
import type { GroupNames } from "./values";

export function PlayPanel({
  projectId,
  boardId,
  doc,
  session,
  returnFocus,
  focusOnOpen = true,
  holding = false,
}: {
  projectId: string;
  /** The board whose page shows the panel. */
  boardId: string;
  doc: ProjectDocument;
  session: PlaySession;
  /** What gets the focus once the panel has gone, as the canvas. */
  returnFocus: () => HTMLElement | null;
  /** Whether the panel's heading takes the focus as it opens (Panel's focusOnOpen). */
  focusOnOpen?: boolean;
  /**
   * Whether the canvas has open what the user types in, the inspector or
   * the editor of a text, which the board's page waits for to close before
   * it follows the flow to another board's page, so that the panel keeps the
   * focus where the flow is meanwhile.
   */
  holding?: boolean;
}) {
  const { t } = useTranslation();
  const state = useStore(session.store);
  // The names of what the panel says where it is, the boards' and the
  // nodes' titles, read alone, so that a change of another node, as one
  // that a collaborator moves, draws nothing here.
  const named = useMemo(() => namedBy(state), [state]);
  const names = useDocumentState(
    doc,
    useShallow((s) => [
      ...named.boards.map((id) => s.view.boards.get(id)?.name),
      ...named.nodes.map((id) => s.view.nodes.get(id)?.title),
    ]),
  );
  // The project's name, which the history's text says.
  const projectName = useDocumentState(doc, (s) => s.view.project?.name ?? "");
  const heading = useRef<HTMLHeadingElement>(null);
  const current = useRef<HTMLElement>(null);
  // The current node's heading gets the focus once a turn has left the
  // flow on the board, as after a choice, a start, or a restart, where the
  // panel had the focus, or nothing had, as once the button pressed went
  // with what the turn showed anew, and on the page of another board that
  // the board's page followed the flow to: so that a keyboard and a screen
  // reader are where the flow is, which the heading names and describes.
  // The page of another board leaves the turn to that board's, which it
  // opens, but for a board that the page no longer holds (followsFlow);
  // while it waits to open it, as the canvas holds what the user types in
  // (holding), the panel gives the current node's heading the focus all the
  // same, as once the choice pressed went with its node, and leaves the turn
  // to that board's page, which takes it once the page opens it.
  const turn = state.status === "playing" ? state.turn : 0;
  const flowBoard = state.status === "playing" ? state.flow.board : "";
  useEffect(() => {
    if (turn === 0) {
      return;
    }
    const follows = followsFlow(doc, flowBoard, boardId);
    if (follows ? !holding : !session.firstShown("panel", turn)) {
      return;
    }
    // The panel's own element, which its heading is in.
    const frame = heading.current?.closest("section");
    if (
      focusIsNowhere(document) ||
      (frame?.contains(document.activeElement) ?? false)
    ) {
      current.current?.focus();
    }
  }, [doc, session, turn, flowBoard, boardId, holding]);
  if (state.status === "idle") {
    return null;
  }
  const boardNames = new Map(named.boards.map((id, i) => [id, names[i]]));
  const nodeTitles = new Map(
    named.nodes.map((id, i) => [id, names[named.boards.length + i]]),
  );
  const played = state.status === "playing" ? state.projection : undefined;
  // The current node's title, as the page has it, as where names it, or as
  // the snapshot has it, of a node that the page no longer has.
  const title =
    state.status === "playing"
      ? titleOf(
          { title: nodeTitles.get(state.flow.node) ?? state.flow.title },
          t,
        )
      : "";
  // Where an entity is, by its node's title and its board's name, as the
  // page has them, or, of what the page no longer has, as a board that a
  // collaborator deleted for good while playing, as the snapshot that play
  // runs has them, linked to the node on its board's canvas, as a finding
  // of the validation is (FR-DEP-03 AC3), while the page has the board.
  const where: Where = (board, node) => {
    const boardName =
      (board === undefined
        ? undefined
        : (boardNames.get(board) ?? played?.boards.get(board))) ??
      board ??
      "";
    const title =
      node === undefined
        ? undefined
        : (nodeTitles.get(node) ?? played?.nodes.get(node)?.title);
    if (title === undefined) {
      return boardName;
    }
    return t("play.where", { node: titleOf({ title }, t), board: boardName });
  };
  // Restart and Try again go as play reads the project anew; the panel's
  // heading has the focus meanwhile, until the current node's has it.
  const restart = () => {
    heading.current?.focus();
    void session.restart();
  };
  const link = (
    board: string | undefined,
    node: string | undefined,
    text: string,
  ) =>
    board === undefined || boardNames.get(board) === undefined ? (
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
      headingRef={heading}
      focusOnOpen={focusOnOpen}
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
            message={
              state.reading
                ? [t("play.failed"), readRefusal(state.error, t)]
                    .filter((m) => m !== undefined)
                    .join(" ")
                : t("play.unplayable")
            }
          />
          {!state.reading && state.error instanceof Error && (
            <code className="block break-words whitespace-pre-wrap text-sm">
              {state.error.message}
            </code>
          )}
          <Button onClick={restart}>{t("play.try_again")}</Button>
        </div>
      ) : (
        <PlayFlowView
          state={state}
          session={session}
          currentRef={current}
          title={title}
          where={where}
          link={link}
          project={projectName}
        />
      )}
      <div className="flex flex-wrap gap-2">
        {state.status === "playing" && (
          <Button onClick={restart}>{t("play.restart")}</Button>
        )}
        <Button onClick={() => session.stop()}>{t("play.stop")}</Button>
      </div>
    </Panel>
  );
}

function PlayFlowView({
  state,
  session,
  currentRef,
  title,
  where,
  link,
  project,
}: {
  state: Extract<PlayState, { status: "playing" }>;
  session: PlaySession;
  /** What gets the focus after a turn: the current node's heading, or what says that the flow is at none. */
  currentRef: RefObject<HTMLElement | null>;
  /** The current node's title, which its heading says. */
  title: string;
  where: Where;
  link: (
    board: string | undefined,
    node: string | undefined,
    text: string,
  ) => ReactNode;
  /** The project's name, which the history's text says. */
  project: string;
}) {
  const { t } = useTranslation();
  const { flow, projection } = state;
  const standIns = projection.standIns;
  // What names the inspector's groups and the history's values: a board as
  // the panel names it, and a component as the snapshot does.
  const groupNames: GroupNames = {
    board: (id) => where(id, undefined),
    component: (id) => projection.componentNames.get(id) ?? id,
  };
  const speakerId = useId();
  const textId = useId();
  const endedId = useId();
  const errorsId = useId();
  // The current node's heading, which gets the focus after a turn, says
  // what follows it as its description, so that a screen reader says the
  // node whole as the focus comes to it.
  const described = [
    ...(flow.speaker === "" ? [] : [speakerId]),
    textId,
    ...(flow.ended === undefined ? [] : [endedId]),
  ].join(" ");
  return (
    <div className="space-y-4">
      {state.changed && (
        // The panel's Restart, under the flow, plays the draft as it is.
        <p role="status" className="rounded-md border border-border p-3">
          {t("play.changed")}
        </p>
      )}
      <section aria-label={t("play.current")} className="space-y-2">
        {flow.node === "" ? (
          <p
            ref={currentRef as RefObject<HTMLParagraphElement | null>}
            tabIndex={-1}
            className="text-fg-muted"
          >
            {t("play.nowhere")}
          </p>
        ) : (
          <>
            <p className="text-sm text-fg-muted">
              {where(flow.board, undefined)}
            </p>
            <h3
              ref={currentRef as RefObject<HTMLHeadingElement | null>}
              tabIndex={-1}
              aria-describedby={described}
              className="font-semibold break-words"
            >
              {title}
            </h3>
            {flow.speaker !== "" && (
              <p id={speakerId} className="font-medium break-words">
                {t("play.speaker", { speaker: flow.speaker })}
              </p>
            )}
            <p id={textId} className="break-words whitespace-pre-line">
              {flow.text}
            </p>
          </>
        )}
        {flow.ended !== undefined && (
          <p id={endedId} className="font-medium">
            {endedMessage(t, flow.ended)}
          </p>
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
      <HiddenChoices session={session} hidden={flow.hidden} />
      {/* Step back, of a flow that has taken a step since its start, and
          Reset, each a turn, after which the current node's heading has
          the focus, as after a choice. */}
      <div className="flex flex-wrap gap-2">
        <Button
          disabled={flow.history.length < 2}
          onClick={() => session.stepBack()}
        >
          {t("play.step_back")}
        </Button>
        <Button onClick={() => session.reset()}>{t("play.reset")}</Button>
      </div>
      {flow.halted && <Alert message={t("play.halted")} />}
      {flow.errors.length > 0 && (
        <section aria-labelledby={errorsId} className="space-y-2">
          <h3 id={errorsId} className="font-semibold">
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
      <Inspector session={session} state={state} names={groupNames} />
      <History
        state={state}
        project={project}
        where={where}
        names={groupNames}
      />
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
              : expressionPlace(
                  t,
                  error.owner,
                  where(error.board, error.node),
                  error.expression,
                ),
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

// readRefusal returns why a read of the project that play waits for failed,
// as a list's read says it, of a request that the server refused or did
// not answer: as too many reads in a minute, which says when Try again
// reads on, from what play has not read yet; or undefined for none, as a
// board that came back from the trash and was not read meanwhile.
function readRefusal(error: unknown, t: TFunction): string | undefined {
  return error instanceof ApiError || error instanceof NetworkError
    ? refusalOf(error, t).message
    : undefined;
}

// namedBy returns the boards and the nodes whose names the panel says, of
// state: the flow's board and its current node, and where its errors and
// the stand-ins are.
function namedBy(state: PlayState): {
  boards: readonly string[];
  nodes: readonly string[];
} {
  const boards = new Set<string>();
  const nodes = new Set<string>();
  if (state.status === "playing") {
    const add = (at: { board?: string; node?: string }) => {
      if (at.board !== undefined) {
        boards.add(at.board);
      }
      if (at.node !== undefined) {
        nodes.add(at.node);
      }
    };
    if (state.flow.board !== "") {
      boards.add(state.flow.board);
    }
    if (state.flow.node !== "") {
      nodes.add(state.flow.node);
    }
    state.flow.errors.forEach(add);
    state.projection.standIns.forEach(add);
    // The nodes that the history lists, and the boards of the variables
    // that the inspector groups by them.
    for (const step of state.flow.history) {
      step.entered.forEach(add);
    }
    for (const v of state.projection.values) {
      if (v.kind === "variable" && v.board !== undefined) {
        boards.add(v.board);
      }
    }
  }
  return { boards: [...boards], nodes: [...nodes] };
}
