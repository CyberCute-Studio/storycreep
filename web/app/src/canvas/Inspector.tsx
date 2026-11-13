// The inspector of a board's canvas (PRD FR-EDIT-02, FR-EDIT-06, plan note
// N68): a panel beside the canvas, or a sheet below 900 pixels
// (NFR-TOUCH-06), of what is selected on it. Of one node it is the node's
// form, of its title, an element's kind, speaker, and whether the flow ends
// at it, and its place and size, which a drag changes too, as fields that
// need no drag (WCAG 2.5.7); its colour, its tags, with the project's as
// suggestions, an element's cover image, and its text, which the canvas's
// editor opens over the node, with the connections that leave an element, a
// branch's outputs, a jumper's target, and the jumpers that target the
// node (InspectorFlow.tsx, plan note N69). Of several, it aligns,
// distributes, colours, and tags them. Of either, it duplicates and deletes
// what is selected. Of the connection selected, in place of nodes, it is
// the connection's form.

import type { Node, NodeUpdatePayload } from "@storycreep/shared/api";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  type FormEvent,
  Fragment,
  type RefObject,
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
import { AssetImage } from "../content/AssetImage";
import {
  ConflictNotice,
  changedOf,
  SaveStatus,
  useEntityForm,
} from "../content/form";
import { parseValue } from "../content/values";
import { hundredths } from "../document/appliers/node";
import { useDocumentState, useProjectDocument } from "../document/react";
import { ExpressionEditor } from "../expr/ExpressionEditor";
import { sourceOf, typedExpression, useTypedSource } from "../expr/fields";
import { colorName, type PaletteColor, paletteColors } from "../project/colors";
import { payloadField } from "../project/dialogs";
import type { Command } from "../project/queries";
import { Button } from "../ui/Button";
import {
  CheckboxField,
  SelectField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert } from "../ui/Page";
import { Panel } from "../ui/Panel";
import type { CanvasActions } from "./actions";
import { maxEdited } from "./commands";
import {
  type Alignment,
  hasControlIn,
  hasPrintedIn,
  hasTagOf,
  maxTags,
  onBoard,
  positionLimit,
  sizeLimit,
  tagCounts,
  tagKey,
  tagsOf,
} from "./edit";
import {
  Choices,
  ConnectionInspector,
  JumperTarget,
  Outputs,
  ReferencedBy,
} from "./InspectorFlow";
import type { FollowLink } from "./Links";
import { titleOf, typeOf } from "./labels";
import { nodeRect } from "./layout";
import type { TargetLookup } from "./targets";

/** What the inspector is given. */
export interface InspectorProps {
  projectId: string;
  /** The selected nodes, as the canvas has them. */
  selection: ReadonlySet<string>;
  /** The connection selected, in place of nodes, if one is. */
  connection?: string;
  actions: CanvasActions;
  /** What tells of a jumper's target that the document does not (targets.ts). */
  targets: TargetLookup;
  /** Selects the connection of id on the canvas, whose inspector this then is. */
  onSelectConnection: (id: string) => void;
  /** Selects the node of id on the canvas, in place of what is selected. */
  onSelectNode: (id: string) => void;
  /** Tells of a click of a link to a node on a board's canvas, which the canvas opens itself where the address names it already (Links.tsx). */
  onFollow: FollowLink;
  /** Opens the editor of the text of the element of id over it on the canvas. */
  onEditText: (id: string) => void;
  onClose: () => void;
  returnFocus: () => HTMLElement | null;
  /** The inspector's heading, which the canvas gives the focus as the user asks again for the inspector that is open (Panel's headingRef). */
  headingRef?: RefObject<HTMLHeadingElement | null>;
}

/** The inspector of what is selected on the canvas. */
export function Inspector({
  projectId,
  selection,
  connection: connectionId,
  actions,
  targets,
  onSelectConnection,
  onSelectNode,
  onFollow,
  onEditText,
  onClose,
  returnFocus,
  headingRef,
}: InspectorProps) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  // The inspector's heading takes the focus as a button of the inspector
  // has it show another thing in place of what the button was of, as a
  // choice's Edit, which shows the connection's form, and which goes with
  // what it was of, as does the button that removes a branch's output, with
  // the output's row, and Try again, of a read of a board's nodes that
  // failed, with what said so: the focus would else fall to the page's
  // content, out of the inspector (WCAG 2.4.3), as it does not where the
  // canvas opens the inspector of what Enter asks for.
  const ownHeading = useRef<HTMLHeadingElement>(null);
  const heading = headingRef ?? ownHeading;
  const toHeading = () => heading.current?.focus();
  const editConnection = (id: string) => {
    onSelectConnection(id);
    toHeading();
  };
  const ids = useMemo(() => [...selection], [selection]);
  // The selected nodes as the document has them, which the inspector shows
  // anew only once one of them changes.
  const nodes = useDocumentState(
    doc,
    useShallow((state) =>
      ids.flatMap((id) => {
        const node = state.view.nodes.get(id);
        return node === undefined || node.deleted_at !== undefined
          ? []
          : [node];
      }),
    ),
  );
  // The connection selected, as the document has it.
  const connection = useDocumentState(doc, (state) => {
    const c =
      connectionId === undefined
        ? undefined
        : state.view.connections.get(connectionId);
    return c?.deleted_at === undefined ? c : undefined;
  });
  const ends = useDocumentState(
    doc,
    useShallow((state) =>
      connection === undefined
        ? []
        : [
            state.view.nodes.get(connection.from_node_id),
            state.view.nodes.get(connection.to_node_id),
          ],
    ),
  );
  const single = nodes.length === 1 ? nodes[0] : undefined;
  const tooMany = nodes.length > maxEdited;
  const untitled = t("boards.untitled_node");
  const title =
    connection !== undefined
      ? t("inspector.connection")
      : single !== undefined
        ? titleOf(single, t)
        : nodes.length === 0
          ? t("inspector.title")
          : t("inspector.selected", { count: nodes.length });
  const subtitle =
    connection !== undefined
      ? t("inspector.connection_ends", {
          from: ends[0] === undefined ? untitled : titleOf(ends[0], t),
          to: ends[1] === undefined ? untitled : titleOf(ends[1], t),
        })
      : (single && typeOf(single, t)) || undefined;
  return (
    <Panel
      title={title}
      subtitle={subtitle}
      onClose={onClose}
      returnFocus={returnFocus}
      headingRef={heading}
    >
      {connection !== undefined ? (
        <ConnectionInspector
          // The parts of one connection, made anew for another.
          key={connection.id}
          projectId={projectId}
          connection={connection}
          actions={actions}
          onSelectNode={(id) => {
            onSelectNode(id);
            toHeading();
          }}
          onDelete={() => {
            void actions.removeConnection(connection.id);
            toHeading();
          }}
        />
      ) : nodes.length === 0 ? (
        <p className="text-fg-muted">{t("inspector.empty")}</p>
      ) : (
        <div className="space-y-6">
          {single !== undefined && (
            // The parts of one node, made anew for another, under one key,
            // rather than a key of the node's for each, which two siblings
            // would share, and which would leave the form of the node
            // selected before beside the next one's.
            <Fragment key={single.id}>
              <NodeForm projectId={projectId} node={single} />
              {single.type !== "jumper" && (
                <NodeScripts projectId={projectId} node={single} />
              )}
              {single.type === "element" && (
                <section className="space-y-2">
                  <h3 className="text-lg font-semibold">
                    {t("inspector.text")}
                  </h3>
                  <Button onClick={() => onEditText(single.id)}>
                    {t("inspector.edit_text")}
                  </Button>
                </section>
              )}
              {single.type === "element" && (
                <Cover projectId={projectId} node={single} />
              )}
              {single.type === "element" && (
                <Choices
                  projectId={projectId}
                  node={single}
                  actions={actions}
                  onSelectConnection={editConnection}
                />
              )}
              {single.type === "branch" && (
                <Outputs
                  projectId={projectId}
                  node={single}
                  actions={actions}
                  onSelectConnection={editConnection}
                  heading={() => heading.current}
                />
              )}
              {single.type === "jumper" && (
                <JumperTarget
                  projectId={projectId}
                  node={single}
                  targets={targets}
                  onFollow={onFollow}
                  heading={() => heading.current}
                />
              )}
              <ReferencedBy
                projectId={projectId}
                node={single}
                onFollow={onFollow}
              />
            </Fragment>
          )}
          {nodes.length > 1 && <Arrange nodes={nodes} actions={actions} />}
          {/* A colour, a tag, and a duplicate are a command for each node,
              of as many as a batch holds, and are offered of no more. */}
          {tooMany && (
            <p className="text-fg-muted">
              {t("inspector.too_many", { most: maxEdited })}
            </p>
          )}
          <ColorChoice nodes={nodes} actions={actions} disabled={tooMany} />
          <Tags
            projectId={projectId}
            nodes={nodes}
            actions={actions}
            disabled={tooMany}
          />
          <section className="space-y-2">
            <h3 className="text-lg font-semibold">{t("inspector.actions")}</h3>
            <div className="flex flex-wrap gap-2">
              {/* Duplicate has the inspector show the copies, once the page
              shows them, in place of the nodes that it was of, and nothing
              meanwhile, as the selection holds the copies to come: the
              heading takes the focus, which the button would else leave to
              the page's content as it goes; and it is offered while no
              copy, paste, or duplicate of the canvas is under way. */}
              <Button
                disabled={tooMany || actions.busy}
                onClick={() => {
                  void actions.duplicate(nodes.map((n) => n.id));
                  toHeading();
                }}
              >
                {t("canvas.duplicate")}
              </Button>
              <Button
                variant="danger"
                onClick={() => void actions.remove(nodes.map((n) => n.id))}
              >
                {t("canvas.delete")}
              </Button>
            </div>
          </section>
        </div>
      )}
    </Panel>
  );
}

const nodeField = payloadField(
  "title",
  "kind",
  "speaker_component_id",
  "is_ending",
  "size.width",
  "size.height",
);

/** What the node's form holds, as it reads it of its fields. */
interface NodeValues {
  title: string;
  kind?: Node["kind"];
  speaker_component_id: string;
  is_ending: boolean;
  x: string;
  y: string;
  automatic: boolean;
  width: string;
  height: string;
}

/** Returns what the node's form shows of node. */
function valuesOf(node: Node): NodeValues {
  const rect = nodeRect(node);
  return {
    title: node.title,
    kind: node.kind,
    speaker_component_id: node.speaker_component_id ?? "",
    is_ending: node.is_ending,
    x: String(node.position.x),
    y: String(node.position.y),
    automatic: node.size === undefined,
    width: String(rect.width),
    height: String(rect.height),
  };
}

/**
 * Returns the number that text spells, as a decimal, with a point and an
 * exponent if any, as a float's value is read (parseValue), or undefined
 * for none, as of a hexadecimal, binary, or octal spelling, which Number
 * would read. Its minus may be U+2212 MINUS SIGN, as the field's message of
 * the bounds writes it, and as a copy of that message pastes it.
 */
function numberOf(text: string): number | undefined {
  const n = parseValue("float", text.replaceAll("\u2212", "-"));
  return typeof n === "number" ? n : undefined;
}

/**
 * The form of one node: its title, an element's kind, speaker, and whether
 * the flow ends at it, and its place and size, which it sends in one batch,
 * as every form of an entity does (useEntityForm), expecting the revision
 * that it shows.
 */
function NodeForm({ projectId, node }: { projectId: string; node: Node }) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const form = useEntityForm(projectId, node, (view, id) => view.nodes.get(id));
  const { commands, held, shown, against } = form;
  const [invalid, setInvalid] = useState<
    Partial<Record<keyof NodeValues, string>>
  >({});
  const refusal = refusalOf(commands.error, t, nodeField);
  useFocusRefusal(held.form, commands.error);
  // Whether the node is sized by its content, as its checkbox says, which
  // the width and height are disabled for: of the form shown, as of its key,
  // and of the node that it shows, as the form shown anew shows it, as once
  // a drag of its handle gave it a size.
  const [sizing, setSizing] = useState<{ key: string; automatic: boolean }>();
  const automatic =
    sizing?.key === held.key ? sizing.automatic : shown.size === undefined;
  const components = useDocumentState(doc, (state) => state.view.components);
  const speakers = useMemo(
    () =>
      [...components.values()]
        .filter((c) => c.deleted_at === undefined)
        .sort((a, b) => a.name.localeCompare(b.name)),
    [components],
  );
  const element = shown.type === "element";
  const values = valuesOf(shown);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const read = (name: string) => String(data.get(name) ?? "");
    const typed: NodeValues = {
      title: read("title").trim(),
      kind: element ? (read("kind") as Node["kind"]) : undefined,
      speaker_component_id: element ? read("speaker") : "",
      is_ending: element && data.get("is_ending") !== null,
      x: read("x"),
      y: read("y"),
      automatic: data.get("automatic") !== null,
      width: read("width"),
      height: read("height"),
    };
    const problems: Partial<Record<keyof NodeValues, string>> = {};
    const x = numberOf(typed.x);
    const y = numberOf(typed.y);
    for (const [field, value] of [
      ["x", x],
      ["y", y],
    ] as const) {
      if (value === undefined || Math.abs(value) > positionLimit) {
        problems[field] = t("inspector.error.position");
      }
    }
    const width = numberOf(typed.width);
    const height = numberOf(typed.height);
    if (!typed.automatic) {
      for (const [field, value] of [
        ["width", width],
        ["height", height],
      ] as const) {
        if (value === undefined || value < 1 || value > sizeLimit) {
          problems[field] = t("inspector.error.size");
        }
      }
    }
    setInvalid(problems);
    if (Object.keys(problems).length > 0) {
      form.refused();
      return;
    }
    // The place and the size as the server keeps them, to 2 decimal places
    // (FR-EDIT-02 AC4), which the document shows at once, and which are
    // compared with the node's as such, so that 10.0 of a node at 10, or
    // 10.001, changes nothing.
    const place = onBoard({ x: x as number, y: y as number });
    typed.x = String(place.x);
    typed.y = String(place.y);
    const size = typed.automatic
      ? undefined
      : {
          width: hundredths(width as number),
          height: hundredths(height as number),
        };
    if (size !== undefined) {
      typed.width = String(size.width);
      typed.height = String(size.height);
    }
    const was = valuesOf(against);
    const changed = changedOf(typed, was);
    const expected_rev = { [against.id]: against.rev };
    // A place, and a size, are each one field of the server's, of two
    // numbers: the one that the user did not change is the node's as the
    // page has it now, rather than as the form holds it, which a drag of the
    // canvas's may have changed since, as the page's own batch, which the
    // save would else write over, unasked, as the page's own batches are no
    // change made elsewhere (the document's ownMoves).
    const now = doc.store.getState().view.nodes.get(shown.id) ?? against;
    const position = {
      x: changed.x === undefined ? now.position.x : place.x,
      y: changed.y === undefined ? now.position.y : place.y,
    };
    const sized =
      size === undefined ||
      changed.automatic !== undefined ||
      now.size === undefined
        ? size
        : {
            width: changed.width === undefined ? now.size.width : size.width,
            height:
              changed.height === undefined ? now.size.height : size.height,
          };
    const update: Omit<NodeUpdatePayload, "id"> = {};
    if (changed.title !== undefined) {
      update.title = typed.title;
    }
    if (element && changed.kind !== undefined) {
      update.kind = typed.kind;
    }
    if (element && changed.speaker_component_id !== undefined) {
      update.speaker_component_id = typed.speaker_component_id;
    }
    if (element && changed.is_ending !== undefined) {
      update.is_ending = typed.is_ending;
    }
    if (
      changed.automatic !== undefined ||
      (!typed.automatic &&
        (changed.width !== undefined || changed.height !== undefined))
    ) {
      update.size = sized ?? {};
    }
    const changes: Command[] = [];
    if (Object.keys(update).length > 0) {
      changes.push({
        type: "node.update",
        payload: { id: shown.id, ...update },
        expected_rev,
      });
    }
    if (changed.x !== undefined || changed.y !== undefined) {
      changes.push({
        type: "node.move",
        payload: { nodes: [{ id: shown.id, position }] },
        expected_rev,
      });
    }
    // What the form holds of the node over the node as it is now, once a
    // save is refused for a change made elsewhere, is the fields that the
    // user changed, by the payload's names.
    const kept: Partial<Node> = {
      ...(update.title === undefined ? {} : { title: update.title }),
      ...(update.kind === undefined ? {} : { kind: update.kind }),
      ...(update.speaker_component_id === undefined
        ? {}
        : {
            speaker_component_id:
              update.speaker_component_id === ""
                ? undefined
                : update.speaker_component_id,
          }),
      ...(update.is_ending === undefined
        ? {}
        : { is_ending: update.is_ending }),
      ...(update.size === undefined
        ? {}
        : {
            size: update.size.width === undefined ? undefined : update.size,
          }),
      ...(changed.x === undefined && changed.y === undefined
        ? {}
        : { position }),
    };
    form.save(changes, kept);
  };
  const fieldName = (field: string) => {
    switch (field) {
      case "title":
        return t("inspector.node_title");
      case "kind":
        return t("inspector.kind");
      case "speaker_component_id":
        return t("inspector.speaker");
      case "is_ending":
        return t("inspector.ending");
      case "size":
        return t("inspector.size");
      default:
        return t("inspector.position");
    }
  };
  const shownValue = (field: string, value: unknown) =>
    field === "title" || field === "kind" ? String(value ?? "") : undefined;
  return (
    <div className="space-y-4">
      <Alert
        message={form.overlaps.length > 0 ? undefined : refusal?.message}
      />
      <ConflictNotice
        form={form}
        fieldName={fieldName}
        shownValue={shownValue}
      />
      <SaveStatus form={form} />
      <form
        key={held.key}
        {...held.props}
        className="space-y-4"
        onSubmit={submit}
        noValidate
      >
        <TextField
          label={t("inspector.node_title")}
          name="title"
          autoComplete="off"
          maxLength={200}
          defaultValue={values.title}
          error={refusal?.fields.title}
        />
        {element && (
          <>
            <SelectField
              label={t("inspector.kind")}
              name="kind"
              defaultValue={values.kind ?? "dialogue"}
              error={refusal?.fields.kind}
            >
              <option value="dialogue">{t("canvas.node.dialogue")}</option>
              <option value="narration">{t("canvas.node.narration")}</option>
            </SelectField>
            <SelectField
              label={t("inspector.speaker")}
              name="speaker"
              defaultValue={values.speaker_component_id}
              error={refusal?.fields.speaker_component_id}
            >
              <option value="">{t("inspector.no_speaker")}</option>
              {/* A speaker that the list does not have, as a component in
                  the trash, shows as it is until another is chosen. */}
              {values.speaker_component_id !== "" &&
                !speakers.some((c) => c.id === values.speaker_component_id) && (
                  <option value={values.speaker_component_id}>
                    {t("inspector.speaker_unknown")}
                  </option>
                )}
              {speakers.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </SelectField>
            <CheckboxField
              label={t("inspector.ending")}
              name="is_ending"
              defaultChecked={values.is_ending}
            />
          </>
        )}
        <fieldset className="space-y-2">
          <legend className="font-medium">{t("inspector.position")}</legend>
          <div className="grid grid-cols-2 gap-2">
            <TextField
              label={t("inspector.x")}
              name="x"
              autoComplete="off"
              spellCheck={false}
              defaultValue={values.x}
              error={invalid.x}
            />
            <TextField
              label={t("inspector.y")}
              name="y"
              autoComplete="off"
              spellCheck={false}
              defaultValue={values.y}
              error={invalid.y}
            />
          </div>
        </fieldset>
        <fieldset className="space-y-2">
          <legend className="font-medium">{t("inspector.size")}</legend>
          <CheckboxField
            label={t("inspector.automatic_size")}
            name="automatic"
            defaultChecked={values.automatic}
            onChange={(e) =>
              setSizing({ key: held.key, automatic: e.currentTarget.checked })
            }
          />
          <div className="grid grid-cols-2 gap-2">
            <TextField
              label={t("inspector.width")}
              name="width"
              autoComplete="off"
              spellCheck={false}
              disabled={automatic}
              defaultValue={values.width}
              error={invalid.width ?? refusal?.fields["size.width"]}
            />
            <TextField
              label={t("inspector.height")}
              name="height"
              autoComplete="off"
              spellCheck={false}
              disabled={automatic}
              defaultValue={values.height}
              error={invalid.height ?? refusal?.fields["size.height"]}
            />
          </div>
        </fieldset>
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("inspector.save")}
        </Button>
      </form>
    </div>
  );
}

const scriptField = payloadField("on_enter", "on_exit");

/**
 * The scripts of an element or a branch (PRD §5.8.5): its on-enter script,
 * which runs as the flow enters it, and its on-exit script, as the flow
 * leaves it, in the expression editor, which Save sends as one node.update
 * of those that changed, expecting the node's revision that the form shows.
 * The empty source is none.
 */
function NodeScripts({ projectId, node }: { projectId: string; node: Node }) {
  const { t } = useTranslation();
  const form = useEntityForm(projectId, node, (view, id) => view.nodes.get(id));
  const { commands, held, shown, against } = form;
  const onEnter = useTypedSource(held);
  const onExit = useTypedSource(held);
  const headingId = useId();
  const refusal = refusalOf(commands.error, t, scriptField);
  useFocusRefusal(held.form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const enter = onEnter.source(shown.on_enter);
    const exit = onExit.source(shown.on_exit);
    const update: Omit<NodeUpdatePayload, "id"> = {
      ...(enter !== sourceOf(against.on_enter) ? { on_enter: enter } : {}),
      ...(exit !== sourceOf(against.on_exit) ? { on_exit: exit } : {}),
    };
    const changes: Command[] =
      Object.keys(update).length === 0
        ? []
        : [
            {
              type: "node.update",
              payload: { id: shown.id, ...update },
              expected_rev: { [against.id]: against.rev },
            },
          ];
    form.save(changes, {
      ...(update.on_enter === undefined
        ? {}
        : { on_enter: typedExpression(update.on_enter, true) }),
      ...(update.on_exit === undefined
        ? {}
        : { on_exit: typedExpression(update.on_exit, true) }),
    });
  };
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h3 id={headingId} className="text-lg font-semibold">
        {t("inspector.scripts")}
      </h3>
      <Alert
        message={form.overlaps.length > 0 ? undefined : refusal?.message}
      />
      <ConflictNotice
        form={form}
        fieldName={(field) =>
          field === "on_enter"
            ? t("inspector.on_enter")
            : t("inspector.on_exit")
        }
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
        <ExpressionEditor
          projectId={projectId}
          boardId={node.board_id}
          context="script"
          label={t("inspector.on_enter")}
          hint={t("inspector.on_enter_hint")}
          defaultValue={sourceOf(shown.on_enter)}
          stored={shown.on_enter}
          error={refusal?.fields.on_enter}
          optional
          onChange={onEnter.onChange}
        />
        <ExpressionEditor
          projectId={projectId}
          boardId={node.board_id}
          context="script"
          label={t("inspector.on_exit")}
          hint={t("inspector.on_exit_hint")}
          defaultValue={sourceOf(shown.on_exit)}
          stored={shown.on_exit}
          error={refusal?.fields.on_exit}
          optional
          onChange={onExit.onChange}
        />
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("inspector.save_scripts")}
        </Button>
      </form>
    </section>
  );
}

/** The alignment and the distribution of several nodes, each one batch. */
function Arrange({
  nodes,
  actions,
}: {
  nodes: readonly Node[];
  actions: CanvasActions;
}) {
  const { t } = useTranslation();
  const ids = nodes.map((n) => n.id);
  const alignments: { how: Alignment; label: string }[] = [
    { how: "left", label: t("inspector.align_left") },
    { how: "center", label: t("inspector.align_center") },
    { how: "right", label: t("inspector.align_right") },
    { how: "top", label: t("inspector.align_top") },
    { how: "middle", label: t("inspector.align_middle") },
    { how: "bottom", label: t("inspector.align_bottom") },
  ];
  return (
    <section className="space-y-4">
      <div className="space-y-2">
        <h3 className="text-lg font-semibold">{t("inspector.align")}</h3>
        <div className="flex flex-wrap gap-2">
          {alignments.map(({ how, label }) => (
            <Button key={how} onClick={() => void actions.align(ids, how)}>
              {label}
            </Button>
          ))}
        </div>
      </div>
      <div className="space-y-2">
        <h3 className="text-lg font-semibold">{t("inspector.distribute")}</h3>
        {nodes.length < 3 && (
          <p className="text-sm text-fg-muted">
            {t("inspector.distribute_hint")}
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <Button
            disabled={nodes.length < 3}
            onClick={() => void actions.distribute(ids, "across")}
          >
            {t("inspector.distribute_across")}
          </Button>
          <Button
            disabled={nodes.length < 3}
            onClick={() => void actions.distribute(ids, "down")}
          >
            {t("inspector.distribute_down")}
          </Button>
        </div>
      </div>
    </section>
  );
}

/** The colour of the selected nodes, which a choice gives each of them at once. */
function ColorChoice({
  nodes,
  actions,
  disabled,
}: {
  nodes: readonly Node[];
  actions: CanvasActions;
  /** Whether a colour may not be given, as of more nodes than one batch colours (maxEdited). */
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const colors = new Set(nodes.map((n) => n.color));
  // Nodes of several colours show none of them chosen.
  const value = colors.size === 1 ? [...colors][0] : "";
  return (
    <SelectField
      label={t("inspector.color")}
      disabled={disabled}
      value={value}
      onChange={(e) => {
        const color = e.currentTarget.value;
        if (color !== "") {
          void actions.color(
            nodes.map((n) => n.id),
            color as PaletteColor,
          );
        }
      }}
    >
      {value === "" && <option value="">{t("inspector.colors_mixed")}</option>}
      {paletteColors.map((color) => (
        <option key={color} value={color}>
          {colorName(t, color)}
        </option>
      ))}
    </SelectField>
  );
}

/** How long the tags' field waits after a key before it asks for suggestions, in milliseconds. */
const suggestDelay = 200;

/**
 * The tags of the selected nodes, each with its button that takes it from
 * every node that has it, and the field that adds one to each, whatever
 * its case, which suggests the project's tags that start as it does, in
 * the spelling that most of their nodes use, the most used first
 * (FR-EDIT-06).
 */
function Tags({
  projectId,
  nodes,
  actions,
  disabled,
}: {
  projectId: string;
  nodes: readonly Node[];
  actions: CanvasActions;
  /** Whether tags may not be added or taken away, as of more nodes than one batch changes (maxEdited). */
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const listId = useId();
  const headingId = useId();
  const [typed, setTyped] = useState("");
  const [prefix, setPrefix] = useState("");
  const [problem, setProblem] = useState<string>();
  useEffect(() => {
    const timer = setTimeout(() => setPrefix(typed.trim()), suggestDelay);
    return () => clearTimeout(timer);
  }, [typed]);
  const suggestions = useQuery({
    queryKey: ["projects", projectId, "tags", prefix],
    queryFn: async ({ signal }) =>
      (
        await api.call(
          "listTags",
          {
            path: { project_id: projectId },
            query: { prefix, limit: 20 },
          },
          { signal },
        )
      ).body.items,
    staleTime: 30_000,
    // A prefix with a character that the server refuses in a tag, as a tab
    // pasted in, which no tag starts with, and which it would refuse the
    // request for, is not asked for: the field says so as the tag is added.
    enabled: !hasControlIn(prefix),
  });
  const ids = nodes.map((n) => n.id);
  // The selection that the field is for now, which a tag refused for
  // another, before the user selected this one, is not put back in, and
  // what the field said of the tag added to another, as of its nodes that
  // have the most tags, it says no longer.
  const forIds = useMemo(() => nodes.map((n) => n.id).join(" "), [nodes]);
  const shownFor = useRef(forIds);
  useEffect(() => {
    if (shownFor.current !== forIds) {
      setProblem(undefined);
    }
    shownFor.current = forIds;
  }, [forIds]);
  // The tags, and how many of the nodes have each, found once for each
  // change of the nodes, rather than for each key typed in the tag field.
  const tags = useMemo(() => tagsOf(nodes), [nodes]);
  const counts = useMemo(() => tagCounts(nodes), [nodes]);
  const count = (tag: string) => counts.get(tagKey(tag)) ?? 0;
  // The suggestions read anew once the project's tags changed, as by a tag
  // added or taken away here, rather than once they are 30 s old.
  const tagsChanged = (applied: boolean) => {
    if (applied) {
      void queryClient.invalidateQueries({
        queryKey: ["projects", projectId, "tags"],
      });
    }
    return applied;
  };
  const add = (event: FormEvent) => {
    event.preventDefault();
    const tag = typed.trim();
    // One that shows no character, as of a zero width space alone, is
    // empty, as the server tells it, and would refuse the tag of every node.
    if (!hasPrintedIn(tag) || [...tag].length > 50) {
      setProblem(t("inspector.error.tag"));
      return;
    }
    // One that the server refuses, as pasted with a bidirectional control,
    // which would refuse the tag of every node, the field says, as it says
    // of a tag too long.
    if (hasControlIn(tag)) {
      setProblem(t("inspector.error.tag_control"));
      return;
    }
    // A node that has the most tags that a node has gets no more, which the
    // field says, while the others that lack the tag get it (addTag); where
    // none of them could, the tag stays in the field.
    const has = hasTagOf(tag);
    const lacking = nodes.filter((n) => !has(n));
    const full = lacking.filter((n) => n.tags.length >= maxTags).length;
    setProblem(
      full === 0
        ? undefined
        : t("inspector.error.tags_full", { count: full, most: maxTags }),
    );
    if (full > 0 && full === lacking.length) {
      return;
    }
    setTyped("");
    // A tag that the server refused, as for a change made elsewhere
    // meanwhile, which the toolbar says, is in the field again, to be
    // changed rather than typed anew, unless the user typed another since,
    // or selected other nodes.
    void actions
      .addTag(ids, tag)
      .then(tagsChanged)
      .then((applied) => {
        if (!applied && shownFor.current === forIds) {
          setTyped((now) => (now === "" ? tag : now));
        }
      });
  };
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h3 id={headingId} className="text-lg font-semibold">
        {t("inspector.tags")}
      </h3>
      {tags.length === 0 ? (
        <p className="text-fg-muted">{t("inspector.no_tags")}</p>
      ) : (
        <ul className="flex flex-wrap gap-2" aria-labelledby={headingId}>
          {tags.map((tag) => (
            <li key={tagKey(tag)}>
              <Button
                aria-label={t("inspector.remove_tag", { tag })}
                disabled={disabled}
                onClick={() =>
                  void actions.removeTag(ids, tag).then(tagsChanged)
                }
              >
                {nodes.length > 1
                  ? t("inspector.tag_count", {
                      tag,
                      count: count(tag),
                      total: nodes.length,
                    })
                  : tag}
                <span aria-hidden="true">×</span>
              </Button>
            </li>
          ))}
        </ul>
      )}
      <form
        onSubmit={add}
        className="flex flex-wrap items-end gap-2"
        noValidate
      >
        <div className="min-w-0 flex-1">
          <TextField
            label={
              nodes.length > 1
                ? t("inspector.add_tag_all")
                : t("inspector.add_tag")
            }
            list={listId}
            value={typed}
            onChange={(e) => setTyped(e.currentTarget.value)}
            maxLength={50}
            autoComplete="off"
            disabled={disabled}
            error={problem}
          />
          <datalist id={listId}>
            {(suggestions.data ?? []).map((s) => (
              <option key={s.tag} value={s.tag} />
            ))}
          </datalist>
        </div>
        <Button type="submit" disabled={disabled}>
          {t("inspector.add")}
        </Button>
      </form>
    </section>
  );
}

/**
 * An element's cover image: its thumbnail, and its upload and removal, each
 * a change of its own, expecting the revision that the inspector showed as
 * the user asked for it, as a component's image is (FR-EDIT-06 AC3,
 * AssetImage).
 */
function Cover({ projectId, node }: { projectId: string; node: Node }) {
  const { t } = useTranslation();
  const title = titleOf(node, t);
  return (
    <AssetImage
      projectId={projectId}
      assetId={node.cover_asset_id}
      rev={node.rev}
      editable
      command={(assetId, rev) => ({
        type: "node.update",
        payload: { id: node.id, cover_asset_id: assetId ?? "" },
        expected_rev: { [node.id]: rev },
      })}
      labels={{
        heading: t("inspector.cover"),
        none: t("inspector.no_cover"),
        alt: t("inspector.cover_alt", { node: title }),
        upload: t("inspector.upload_cover"),
        replace: t("inspector.replace_cover"),
        remove: t("inspector.remove_cover"),
      }}
    />
  );
}
