// What the canvas's inspector shows of a board's flow (PRD FR-EDIT-03,
// FR-EDIT-04, FR-EDIT-05, plan note N69): of an element, the connections
// that leave it, its choices, in their order, which it reorders, by a drag
// of a row's handle or by its buttons, and adds to; of a branch, its
// outputs, which it adds, removes, and reorders, each with the connections
// that leave it; of a jumper, its target, which it sets, and its link to
// it; of any node, the jumpers that target it; and of the connection
// selected, its label, the port that it leaves and the node that it
// enters, its place among its port's choices, and its deletion. Each does
// without a drag what a drag of a link handle does on the canvas (WCAG
// 2.5.7). The conditions of a connection and of an output, and a
// connection's script, are the expression editor's (plan item 67).

import type { BranchOutput, Connection, Node } from "@storycreep/shared/api";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink } from "@tanstack/react-router";
import {
  type FormEvent,
  type PointerEvent,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { useShallow } from "zustand/react/shallow";
import { api } from "../api";
import { refusalOf } from "../auth/refusal";
import { ConflictNotice, SaveStatus, useEntityForm } from "../content/form";
import { boardPart } from "../document/doc";
import { useDocumentState, useProjectDocument } from "../document/react";
import { equal } from "../document/reconcile";
import { Confirm, payloadField } from "../project/dialogs";
import { type Command, loadPart } from "../project/queries";
import type { RichTextValue } from "../richtext/convert";
import { RichTextEditor } from "../richtext/RichTextEditor";
import { Button } from "../ui/Button";
import { dragThreshold } from "../ui/drag";
import { SelectField, useFocusRefusal } from "../ui/Field";
import { Alert, textLink } from "../ui/Page";
import type { CanvasActions } from "./actions";
import { choicesOf, conditionalsOf, keyAt, reconnectCommand } from "./flow";
import {
  type LabelContext,
  outputLabel,
  plainText,
  targetLabel,
  titleOf,
  typeOf,
} from "./labels";
import { type TargetLookup, targetOf } from "./targets";

/** How many of the jumpers that target a node its inspector lists at most. */
const referrersShown = 100;

/** Returns what names node in a list of a board's nodes: its title, as its card shows it, and its type. */
function nodeOption(node: Node, t: LabelContext["t"]): string {
  return `${titleOf(node, t)} (${typeOf(node, t)})`;
}

/** Returns the nodes of the board of boardId that nodes holds, by what names them, in the order of their names. */
function namedNodes(
  nodes: ReadonlyMap<string, Node>,
  boardId: string,
  t: LabelContext["t"],
): { node: Node; name: string }[] {
  const out: { node: Node; name: string }[] = [];
  for (const node of nodes.values()) {
    if (node.board_id === boardId && node.deleted_at === undefined) {
      out.push({ node, name: nodeOption(node, t) });
    }
  }
  return out.sort(
    (a, b) => a.name.localeCompare(b.name) || (a.node.id < b.node.id ? -1 : 1),
  );
}

/**
 * Returns the drag that reorders the rows of a list by their handles
 * (FR-EDIT-03): a press of a row's handle, once it has moved 8 px
 * (NFR-TOUCH-05), holds the row, which lands where it is let go, among the
 * rows whose middles are above the pointer, as onMove is told; Escape, and
 * a pointer that the browser takes, leave it where it was. The rows' own
 * buttons move them without a drag, so the handles are hidden from
 * assistive technology.
 */
function useReorderDrag(onMove: (from: number, to: number) => void) {
  const list = useRef<HTMLOListElement>(null);
  const press = useRef<{ id: number; from: number; y: number; held: boolean }>(
    undefined,
  );
  const [drag, setDrag] = useState<{ from: number; to: number }>();
  /** Returns the place that the row of from would land at, with the pointer at y of the window. */
  const placeAt = (from: number, y: number): number => {
    let to = 0;
    Array.from(list.current?.children ?? []).forEach((row, i) => {
      const r = row.getBoundingClientRect();
      if (i !== from && y > r.top + r.height / 2) {
        to++;
      }
    });
    return to;
  };
  const end = () => {
    press.current = undefined;
    setDrag(undefined);
  };
  const dragging = drag !== undefined;
  useEffect(() => {
    if (!dragging) {
      return;
    }
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        press.current = undefined;
        setDrag(undefined);
      }
    };
    window.addEventListener("keydown", key, true);
    return () => window.removeEventListener("keydown", key, true);
  }, [dragging]);
  return {
    list,
    drag,
    /** The props of the handle of the row at from. */
    handle: (from: number) => ({
      onPointerDown: (e: PointerEvent<HTMLElement>) => {
        if (e.button !== 0) {
          return;
        }
        press.current = { id: e.pointerId, from, y: e.clientY, held: false };
        e.currentTarget.setPointerCapture?.(e.pointerId);
        e.preventDefault();
      },
      onPointerMove: (e: PointerEvent<HTMLElement>) => {
        const p = press.current;
        if (p === undefined || p.id !== e.pointerId) {
          return;
        }
        if (!p.held && Math.abs(e.clientY - p.y) < dragThreshold) {
          return;
        }
        p.held = true;
        const to = placeAt(p.from, e.clientY);
        setDrag((d) =>
          d?.from === p.from && d.to === to ? d : { from: p.from, to },
        );
      },
      onPointerUp: (e: PointerEvent<HTMLElement>) => {
        const p = press.current;
        if (p === undefined || p.id !== e.pointerId) {
          return;
        }
        const to = p.held ? placeAt(p.from, e.clientY) : p.from;
        end();
        if (to !== p.from) {
          onMove(p.from, to);
        }
      },
      onPointerCancel: end,
      onLostPointerCapture: end,
    }),
  };
}

/** The classes of a row's handle: of a finger's size, and the pointer's alone, so that a finger that drags it does not scroll the panel. */
const dragHandle =
  "inline-flex min-h-11 min-w-11 cursor-grab touch-none items-center justify-center rounded-md text-fg-muted select-none";

/**
 * The connections that leave port of node, the choices that the port
 * offers, in their order (FR-EDIT-03 AC1): each with what it is labelled
 * and the node that it enters, a button that selects it, whose inspector
 * changes it, and its place, which a drag of its handle, or its buttons,
 * change; and a list of the board's nodes, one of which Connect connects
 * the port to.
 */
export function PortChoices({
  projectId,
  node,
  port,
  heading,
  actions,
  onSelectConnection,
}: {
  projectId: string;
  node: Node;
  port: string;
  /** What names the choices, as the heading of an element's, or of which output they are. */
  heading: string;
  actions: CanvasActions;
  onSelectConnection: (id: string) => void;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const headingId = useId();
  const choices = useDocumentState(
    doc,
    useShallow((state) => choicesOf(state.view, node.id, port)),
  );
  const nodes = useDocumentState(doc, (state) => state.view.nodes);
  const components = useDocumentState(doc, (state) => state.view.components);
  const variables = useDocumentState(doc, (state) => state.view.variables);
  const targets = useMemo(
    () => namedNodes(nodes, node.board_id, t),
    [nodes, node.board_id, t],
  );
  const [to, setTo] = useState("");
  const reorder = useReorderDrag(
    (from, place) => void actions.moveChoice(choices[from].id, place),
  );
  const names = {
    componentName: (id: string) => components.get(id)?.name,
    variableKey: (id: string) => variables.get(id)?.key,
  };
  const connect = (event: FormEvent) => {
    event.preventDefault();
    if (to !== "") {
      void actions.connect({ node: node.id, port }, to);
      setTo("");
    }
  };
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h4 id={headingId} className="font-medium">
        {heading}
      </h4>
      {choices.length === 0 ? (
        <p className="text-fg-muted">{t("inspector.no_choices")}</p>
      ) : (
        <ol
          ref={reorder.list}
          aria-labelledby={headingId}
          className="space-y-1"
        >
          {choices.map((choice, i) => {
            const label = plainText(choice.label, names).replace(/\s+/g, " ");
            const target = nodes.get(choice.to_node_id);
            const drag = reorder.drag;
            const lands =
              drag === undefined || drag.to === drag.from || drag.to !== i
                ? ""
                : drag.to < drag.from
                  ? "border-t-accent"
                  : "border-b-accent";
            return (
              <li
                key={choice.id}
                data-choice={choice.id}
                className={`flex flex-wrap items-center gap-2 border-y-2 border-transparent ${lands} ${
                  drag?.from === i ? "opacity-60" : ""
                }`}
              >
                <span
                  aria-hidden="true"
                  data-drag=""
                  className={dragHandle}
                  {...reorder.handle(i)}
                >
                  ⠿
                </span>
                <span className="min-w-0 flex-1 break-words">
                  {t("inspector.choice", {
                    place: i + 1,
                    label:
                      label === "" ? t("inspector.choice_unlabelled") : label,
                    target:
                      target === undefined
                        ? t("boards.untitled_node")
                        : titleOf(target, t),
                  })}
                </span>
                <Button
                  aria-label={t("inspector.choice_edit", { place: i + 1 })}
                  onClick={() => onSelectConnection(choice.id)}
                >
                  {t("inspector.edit")}
                </Button>
                <Button
                  aria-label={t("inspector.choice_up", { place: i + 1 })}
                  disabled={keyAt(choices, i, i - 1) === undefined}
                  onClick={() => void actions.moveChoice(choice.id, i - 1)}
                >
                  <span aria-hidden="true">↑</span>
                </Button>
                <Button
                  aria-label={t("inspector.choice_down", { place: i + 1 })}
                  disabled={keyAt(choices, i, i + 1) === undefined}
                  onClick={() => void actions.moveChoice(choice.id, i + 1)}
                >
                  <span aria-hidden="true">↓</span>
                </Button>
              </li>
            );
          })}
        </ol>
      )}
      <form
        onSubmit={connect}
        className="flex flex-wrap items-end gap-2"
        noValidate
      >
        <div className="min-w-0 flex-1">
          <SelectField
            label={t("inspector.connect_to")}
            value={to}
            onChange={(e) => setTo(e.currentTarget.value)}
          >
            <option value="">{t("inspector.choose_node")}</option>
            {targets.map(({ node: target, name }) => (
              <option key={target.id} value={target.id}>
                {name}
              </option>
            ))}
          </SelectField>
        </div>
        <Button type="submit" disabled={to === ""}>
          {t("inspector.connect")}
        </Button>
      </form>
    </section>
  );
}

/**
 * A branch's outputs, in their order (FR-EDIT-04): each with its kind and
 * its condition, as its row on the branch's card says them, its place
 * among the conditional outputs, which its buttons change, its removal,
 * which asks first where connections leave it, as they are deleted with it
 * (AC3), and the connections that leave it; and what adds a conditional
 * output, or the else, which a branch has one of at most, and only while it
 * has a conditional output.
 */
export function Outputs({
  projectId,
  node,
  actions,
  onSelectConnection,
}: {
  projectId: string;
  node: Node;
  actions: CanvasActions;
  onSelectConnection: (id: string) => void;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const outputs = node.outputs ?? [];
  const conditionals = conditionalsOf(node);
  const hasElse = outputs.some((o) => o.kind === "else");
  // The output whose removal asks first, with the connections that leave
  // it, and the button that asked for it.
  const [removing, setRemoving] = useState<{
    output: BranchOutput;
    connections: number;
    opener: HTMLElement;
    pending: boolean;
  }>();
  const remove = (output: BranchOutput, opener: HTMLElement) => {
    const connections = choicesOf(
      doc.store.getState().view,
      node.id,
      output.id,
    ).length;
    if (connections === 0) {
      void actions.removeOutput(node.id, output.id, false);
    } else {
      setRemoving({ output, connections, opener, pending: false });
    }
  };
  return (
    <section className="space-y-4">
      <h3 className="text-lg font-semibold">{t("inspector.outputs")}</h3>
      {outputs.length === 0 ? (
        <p className="text-fg-muted">{t("canvas.node.no_outputs")}</p>
      ) : (
        <ol className="space-y-4">
          {outputs.map((output) => {
            const name = outputLabel(output, t);
            const place = conditionals.findIndex((o) => o.id === output.id);
            // The only conditional output of a branch that has an else
            // stays, as the else needs one: the else is removed first.
            const needed =
              output.kind !== "else" && conditionals.length === 1 && hasElse;
            return (
              <li
                key={output.id}
                data-output={output.id}
                className="space-y-2 rounded-md border border-border p-3"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <span className="min-w-0 flex-1 font-medium break-words">
                    {name}
                  </span>
                  {place >= 0 && (
                    <>
                      <Button
                        aria-label={t("inspector.output_up", { output: name })}
                        disabled={
                          keyAt(conditionals, place, place - 1) === undefined
                        }
                        onClick={() =>
                          void actions.moveOutput(node.id, output.id, place - 1)
                        }
                      >
                        <span aria-hidden="true">↑</span>
                      </Button>
                      <Button
                        aria-label={t("inspector.output_down", {
                          output: name,
                        })}
                        disabled={
                          keyAt(conditionals, place, place + 1) === undefined
                        }
                        onClick={() =>
                          void actions.moveOutput(node.id, output.id, place + 1)
                        }
                      >
                        <span aria-hidden="true">↓</span>
                      </Button>
                    </>
                  )}
                  <Button
                    aria-label={t("inspector.output_remove", { output: name })}
                    disabled={needed}
                    onClick={(e) => remove(output, e.currentTarget)}
                  >
                    {t("inspector.remove")}
                  </Button>
                </div>
                {needed && (
                  <p className="text-sm text-fg-muted">
                    {t("inspector.output_needed")}
                  </p>
                )}
                <PortChoices
                  projectId={projectId}
                  node={node}
                  port={output.id}
                  heading={t("inspector.output_choices")}
                  actions={actions}
                  onSelectConnection={onSelectConnection}
                />
              </li>
            );
          })}
        </ol>
      )}
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => void actions.addOutput(node.id, false)}>
          {t("inspector.add_output")}
        </Button>
        <Button
          disabled={hasElse || conditionals.length === 0}
          onClick={() => void actions.addOutput(node.id, true)}
        >
          {t("inspector.add_else")}
        </Button>
      </div>
      {removing !== undefined && (
        <Confirm
          title={t("inspector.remove_output_title")}
          description={t("inspector.remove_output_connected", {
            count: removing.connections,
          })}
          confirm={t("inspector.remove_output")}
          opener={removing.opener}
          pending={removing.pending}
          onClose={() => setRemoving(undefined)}
          onConfirm={() => {
            setRemoving({ ...removing, pending: true });
            // The dialog closes once the removal has answered, applied or
            // refused, which the canvas says.
            void actions
              .removeOutput(node.id, removing.output.id, true)
              .finally(() => setRemoving(undefined));
          }}
        >
          {null}
        </Confirm>
      )}
    </section>
  );
}

const targetField = payloadField("target", "target.board_id", "target.node_id");

/**
 * A jumper's target (FR-EDIT-05): what it is now, with the link that opens
 * it, or that it was deleted; and a board of the project and a node of it,
 * or none, which Save gives the jumper, as one node.update, expecting the
 * revision that the form shows. The nodes of a board that the page has not
 * read are read as the board is chosen.
 */
export function JumperTarget({
  projectId,
  node,
  targets,
}: {
  projectId: string;
  node: Node;
  targets: TargetLookup;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const form = useEntityForm(projectId, node, (view, id) => view.nodes.get(id));
  const { commands, held, shown, against } = form;
  const refusal = refusalOf(commands.error, t, targetField);
  useFocusRefusal(held.form, commands.error);
  const boards = useDocumentState(doc, (state) => state.view.boards);
  const nodes = useDocumentState(doc, (state) => state.view.nodes);
  const boardList = useMemo(
    () =>
      [...boards.values()]
        .filter((b) => b.deleted_at === undefined)
        .sort((a, b) => a.name.localeCompare(b.name) || (a.id < b.id ? -1 : 1)),
    [boards],
  );
  // The board chosen, of the form shown, as of its key, and else the board
  // of the target that the form shows.
  const [choice, setChoice] = useState<{ key: string; board: string }>();
  const board =
    choice?.key === held.key ? choice.board : (shown.target?.board_id ?? "");
  // The read of the chosen board's nodes, where the document has not read
  // them: under way, or failed, which Try again reads again.
  const [read, setRead] = useState<{
    board: string;
    state: "loading" | "failed";
  }>();
  const [attempt, setAttempt] = useState(0);
  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt reads the board's nodes again, as the user asks for it.
  useEffect(() => {
    if (board === "" || doc.hasRead(boardPart(board))) {
      setRead(undefined);
      return;
    }
    let ended = false;
    setRead({ board, state: "loading" });
    loadPart(doc, boardPart(board)).then(
      () => {
        // The board's content, read, tells of the targets on it, as that
        // one that it lacks is gone, of this jumper and of the others.
        targets.boardWasRead?.();
        if (!ended) {
          setRead(undefined);
        }
      },
      () => {
        if (!ended) {
          setRead({ board, state: "failed" });
        }
      },
    );
    return () => {
      ended = true;
    };
  }, [doc, board, attempt, targets]);
  const reading = read?.board === board ? read.state : undefined;
  const options = useMemo(
    () =>
      board === ""
        ? []
        : namedNodes(nodes, board, t).filter((o) => o.node.id !== node.id),
    [nodes, board, node.id, t],
  );
  const [problem, setProblem] = useState<string>();
  const current = targetOf(shown, { nodes, boards }, targets);
  const shownNode =
    shown.target?.board_id === board ? (shown.target.node_id ?? "") : "";
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const boardId = String(data.get("board") ?? "");
    const nodeId = String(data.get("node") ?? "");
    if (boardId !== "" && nodeId === "") {
      setProblem(t("inspector.error.target_node"));
      form.refused();
      return;
    }
    setProblem(undefined);
    const target =
      boardId === "" ? undefined : { board_id: boardId, node_id: nodeId };
    const changes: Command[] =
      target?.node_id === against.target?.node_id &&
      target?.board_id === against.target?.board_id
        ? []
        : [
            {
              type: "node.update",
              payload: { id: shown.id, target: target ?? {} },
              expected_rev: { [against.id]: against.rev },
            },
          ];
    form.save(changes, changes.length === 0 ? {} : { target });
  };
  return (
    <section className="space-y-4">
      <h3 className="text-lg font-semibold">{t("inspector.target")}</h3>
      <p className={current.state === "gone" ? "text-danger" : undefined}>
        {targetLabel(current, {
          t,
          boardName: (id) => boards.get(id)?.name,
        })}
        {(current.state === "found" || current.state === "unknown") &&
          current.boardId !== undefined && (
            <>
              {" · "}
              <RouterLink
                to="/projects/$projectId/boards/$boardId"
                params={{ projectId, boardId: current.boardId }}
                search={{ node: current.nodeId }}
                className={textLink}
              >
                {t("canvas.go_to_target")}
              </RouterLink>
            </>
          )}
      </p>
      <Alert
        message={form.overlaps.length > 0 ? undefined : refusal?.message}
      />
      <ConflictNotice
        form={form}
        fieldName={() => t("inspector.target")}
        shownValue={() => undefined}
      />
      <SaveStatus form={form} />
      <form
        key={held.key}
        {...held.props}
        className="space-y-4"
        onSubmit={submit}
        noValidate
      >
        <SelectField
          label={t("inspector.target_board")}
          name="board"
          value={board}
          onChange={(e) => {
            setChoice({ key: held.key, board: e.currentTarget.value });
            setProblem(undefined);
          }}
          error={refusal?.fields["target.board_id"]}
        >
          <option value="">{t("inspector.no_target")}</option>
          {/* A board that the list does not have, as one in the trash,
              shows as it is until another is chosen. */}
          {board !== "" && !boardList.some((b) => b.id === board) && (
            <option value={board}>{t("inspector.target_board_gone")}</option>
          )}
          {boardList.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
        </SelectField>
        {board !== "" && (
          <SelectField
            // The nodes of another board are another list, which starts
            // with none chosen.
            key={board}
            label={t("inspector.target_node")}
            name="node"
            defaultValue={shownNode}
            disabled={reading === "loading"}
            error={
              problem ??
              refusal?.fields["target.node_id"] ??
              refusal?.fields.target
            }
          >
            <option value="">{t("inspector.choose_node")}</option>
            {/* A node that the list does not have, as one deleted, shows
                as it is until another is chosen. */}
            {shownNode !== "" &&
              !options.some((o) => o.node.id === shownNode) && (
                <option value={shownNode}>
                  {reading === "loading"
                    ? t("inspector.target_node_reading")
                    : t("inspector.target_node_gone")}
                </option>
              )}
            {options.map(({ node: option, name }) => (
              <option key={option.id} value={option.id}>
                {name}
              </option>
            ))}
          </SelectField>
        )}
        {reading === "failed" && (
          <p role="alert" className="flex flex-wrap items-center gap-2">
            {t("inspector.target_nodes_failed")}
            <Button onClick={() => setAttempt((n) => n + 1)}>
              {t("canvas.retry")}
            </Button>
          </p>
        )}
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("inspector.save_target")}
        </Button>
      </form>
    </section>
  );
}

/**
 * The jumpers that target node, on any board of the project (FR-EDIT-05
 * AC2), the first of them, each a link that opens it on its board's
 * canvas: read as the inspector shows the node, and again once a jumper
 * that the page holds comes to target it, or no longer does.
 */
export function ReferencedBy({
  projectId,
  node,
}: {
  projectId: string;
  node: Node;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const headingId = useId();
  const boards = useDocumentState(doc, (state) => state.view.boards);
  // The jumpers that the page holds that target the node, which the list
  // is read again for as they change.
  const held = useDocumentState(doc, (state) => {
    const ids: string[] = [];
    for (const other of state.view.nodes.values()) {
      if (
        other.type === "jumper" &&
        other.target?.node_id === node.id &&
        other.deleted_at === undefined
      ) {
        ids.push(other.id);
      }
    }
    return ids.sort().join(" ");
  });
  const list = useQuery({
    queryKey: ["projects", projectId, "nodes", node.id, "referrers", held],
    // The section shows a refusal of it.
    meta: { showsRefusals: true },
    queryFn: async ({ signal }) => {
      const page = (
        await api.call(
          "listNodeReferrers",
          {
            path: { project_id: projectId, node_id: node.id },
            query: { limit: referrersShown },
          },
          { signal },
        )
      ).body;
      return { items: page.items, more: page.next_cursor !== null };
    },
    // What the section lists is what the node has as it is shown.
    staleTime: 0,
    gcTime: 0,
  });
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h3 id={headingId} className="text-lg font-semibold">
        {t("inspector.referenced_by")}
      </h3>
      {list.isPending ? (
        <p className="text-fg-muted">{t("usage.loading")}</p>
      ) : list.isError ? (
        <Alert message={refusalOf(list.error, t)?.message} />
      ) : list.data.items.length === 0 ? (
        <p className="text-fg-muted">{t("inspector.no_referrers")}</p>
      ) : (
        <>
          <ul className="list-disc space-y-1 ps-5" aria-labelledby={headingId}>
            {list.data.items.map((jumper) => (
              <li key={jumper.id} className="break-words">
                <RouterLink
                  to="/projects/$projectId/boards/$boardId"
                  params={{ projectId, boardId: jumper.board_id }}
                  search={{ node: jumper.id }}
                  className={textLink}
                >
                  {titleOf(jumper, t)}
                </RouterLink>
                {" · "}
                {boards.get(jumper.board_id)?.name ?? t("boards.unknown_board")}
              </li>
            ))}
          </ul>
          {list.data.more && (
            <p className="text-sm text-fg-muted">
              {t("inspector.more_referrers", { count: referrersShown })}
            </p>
          )}
        </>
      )}
    </section>
  );
}

const connectionField = payloadField(
  "label",
  "from_node_id",
  "from_port",
  "to_node_id",
);

/** Returns the value of the choice of a port in a list of ports: its node's ID and the port. */
function portValue(nodeId: string, port: string): string {
  return `${nodeId} ${port}`;
}

/**
 * The form of the connection selected (FR-EDIT-03): its label, the choice
 * that the player is offered, a rich text, the port that it leaves, of an
 * element or of a branch's output of its board, and the node of the board
 * that it enters, which Save sends in one batch, a connection.update of
 * the label and a connection.reconnect of the ends that changed, each
 * expecting the revision that the form shows.
 */
function ConnectionForm({
  projectId,
  connection,
}: {
  projectId: string;
  connection: Connection;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const form = useEntityForm(projectId, connection, (view, id) =>
    view.connections.get(id),
  );
  const { commands, held, shown, against } = form;
  // What the editor of the label gave last, of the form shown now.
  const label = useRef<{ key: string; value: RichTextValue }>(undefined);
  const [invalid, setInvalid] = useState<{ fields: Record<string, string> }>();
  const refusal = refusalOf(commands.error, t, connectionField);
  useFocusRefusal(held.form, invalid ?? commands.error);
  const nodes = useDocumentState(doc, (state) => state.view.nodes);
  const components = useDocumentState(doc, (state) => state.view.components);
  const variables = useDocumentState(doc, (state) => state.view.variables);
  const names = useMemo(
    () => ({
      mention: (id: string) => components.get(id)?.name,
      value: (ref: string, attribute: string | undefined) =>
        attribute === undefined ? variables.get(ref)?.key : undefined,
    }),
    [components, variables],
  );
  const named = useMemo(
    () => namedNodes(nodes, connection.board_id, t),
    [nodes, connection.board_id, t],
  );
  // The ports that a connection may leave: an element's one, and each
  // output of a branch.
  const ports = useMemo(
    () =>
      named.flatMap(({ node, name }) =>
        node.type === "element"
          ? [{ value: portValue(node.id, "out"), name }]
          : node.type === "branch"
            ? (node.outputs ?? []).map((output) => ({
                value: portValue(node.id, output.id),
                name: t("inspector.port_option", {
                  node: name,
                  output: outputLabel(output, t),
                }),
              }))
            : [],
      ),
    [named, t],
  );
  const from = portValue(shown.from_node_id, shown.from_port);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const typedLabel =
      label.current?.key === held.key ? label.current.value : undefined;
    if (typedLabel?.error !== undefined) {
      // The editor says why the server would refuse the label, under it.
      form.refused();
      setInvalid({ fields: {} });
      return;
    }
    setInvalid(undefined);
    const text = typedLabel?.doc ?? shown.label;
    const [fromNode, fromPort] = String(data.get("from") ?? from).split(" ");
    const toNode = String(data.get("to") ?? shown.to_node_id);
    const expected_rev = { [against.id]: against.rev };
    const changes: Command[] = [];
    const labelChanged = !equal(text, against.label);
    if (labelChanged) {
      changes.push({
        type: "connection.update",
        payload: { id: shown.id, label: text },
        expected_rev,
      });
    }
    const leaves =
      fromNode !== against.from_node_id || fromPort !== against.from_port;
    const enters = toNode !== against.to_node_id;
    if (leaves || enters) {
      changes.push(
        reconnectCommand(
          shown.id,
          {
            ...(leaves ? { from: { node: fromNode, port: fromPort } } : {}),
            ...(enters ? { to: toNode } : {}),
          },
          against.rev,
        ),
      );
    }
    form.save(changes, {
      ...(labelChanged ? { label: text } : {}),
      ...(leaves ? { from_node_id: fromNode, from_port: fromPort } : {}),
      ...(enters ? { to_node_id: toNode } : {}),
    });
  };
  const fieldName = (field: string) =>
    field === "label"
      ? t("inspector.label")
      : field === "to_node_id"
        ? t("inspector.enters")
        : field === "order"
          ? t("inspector.place")
          : t("inspector.leaves");
  return (
    <div className="space-y-4">
      <Alert
        message={form.overlaps.length > 0 ? undefined : refusal?.message}
      />
      <ConflictNotice
        form={form}
        fieldName={fieldName}
        shownValue={() => undefined}
      />
      <SaveStatus form={form} />
      <form
        key={held.key}
        {...held.props}
        className="space-y-4"
        onSubmit={submit}
        noValidate
      >
        <RichTextEditor
          label={t("inspector.label")}
          hint={t("inspector.label_hint")}
          defaultValue={shown.label}
          error={refusal?.fields.label}
          names={names}
          onChange={(value) => {
            label.current = { key: held.key, value };
            held.props.onChange();
          }}
        />
        <SelectField
          label={t("inspector.leaves")}
          name="from"
          defaultValue={from}
          error={refusal?.fields.from_node_id ?? refusal?.fields.from_port}
        >
          {/* A port that the list does not have, as of an output deleted
              since, shows as it is until another is chosen. */}
          {!ports.some((p) => p.value === from) && (
            <option value={from}>{t("inspector.port_gone")}</option>
          )}
          {ports.map((p) => (
            <option key={p.value} value={p.value}>
              {p.name}
            </option>
          ))}
        </SelectField>
        <SelectField
          label={t("inspector.enters")}
          name="to"
          defaultValue={shown.to_node_id}
          error={refusal?.fields.to_node_id}
        >
          {!named.some((n) => n.node.id === shown.to_node_id) && (
            <option value={shown.to_node_id}>
              {t("inspector.target_node_gone")}
            </option>
          )}
          {named.map(({ node, name }) => (
            <option key={node.id} value={node.id}>
              {name}
            </option>
          ))}
        </SelectField>
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("inspector.save_connection")}
        </Button>
      </form>
    </div>
  );
}

/**
 * The inspector of the connection selected: its form, its place among the
 * choices of the port that it leaves, which its buttons change (FR-EDIT-03
 * AC2), the buttons that select the node at either end, and its deletion.
 */
export function ConnectionInspector({
  projectId,
  connection,
  actions,
  onSelectNode,
}: {
  projectId: string;
  connection: Connection;
  actions: CanvasActions;
  onSelectNode: (id: string) => void;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const choices = useDocumentState(
    doc,
    useShallow((state) =>
      choicesOf(state.view, connection.from_node_id, connection.from_port),
    ),
  );
  const place = choices.findIndex((c) => c.id === connection.id);
  return (
    <div className="space-y-6">
      <ConnectionForm projectId={projectId} connection={connection} />
      <section className="space-y-2">
        <h3 className="text-lg font-semibold">{t("inspector.place")}</h3>
        <p>
          {t("inspector.choice_place", {
            place: place + 1,
            count: choices.length,
          })}
        </p>
        <div className="flex flex-wrap gap-2">
          <Button
            disabled={keyAt(choices, place, place - 1) === undefined}
            onClick={() => void actions.moveChoice(connection.id, place - 1)}
          >
            {t("inspector.move_earlier")}
          </Button>
          <Button
            disabled={keyAt(choices, place, place + 1) === undefined}
            onClick={() => void actions.moveChoice(connection.id, place + 1)}
          >
            {t("inspector.move_later")}
          </Button>
        </div>
      </section>
      <section className="space-y-2">
        <h3 className="text-lg font-semibold">{t("inspector.actions")}</h3>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => onSelectNode(connection.from_node_id)}>
            {t("inspector.select_from")}
          </Button>
          <Button onClick={() => onSelectNode(connection.to_node_id)}>
            {t("inspector.select_to")}
          </Button>
          <Button
            variant="danger"
            onClick={() => void actions.removeConnection(connection.id)}
          >
            {t("inspector.delete_connection")}
          </Button>
        </div>
      </section>
    </div>
  );
}
