// The inspector of a board's canvas (PRD FR-EDIT-02, FR-EDIT-06, plan note
// N68): a panel beside the canvas, or a sheet below 900 pixels
// (NFR-TOUCH-06), of what is selected on it. Of one node it is the node's
// form, of its title, an element's kind, speaker, and whether the flow ends
// at it, and its place and size, which a drag changes too, as fields that
// need no drag (WCAG 2.5.7); its colour, its tags, with the project's as
// suggestions, an element's cover image, and its text, which the canvas's
// editor opens over the node. Of several, it aligns, distributes, colours,
// and tags them. Of either, it duplicates and deletes what is selected.

import type { Node, NodeUpdatePayload } from "@storycreep/shared/api";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  type ChangeEvent,
  type FormEvent,
  useEffect,
  useId,
  useMemo,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { useShallow } from "zustand/react/shallow";
import { api, sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { csrfHeaders } from "../auth/session";
import { imageTypes, thumbnailOf } from "../content/assets";
import {
  ConflictNotice,
  changedOf,
  SaveStatus,
  useEntityForm,
} from "../content/form";
import { useDocumentState, useProjectDocument } from "../document/react";
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
import {
  type Alignment,
  positionLimit,
  sizeLimit,
  tagKey,
  tagsOf,
} from "./edit";
import { nodeRect } from "./layout";

/** What the inspector is given. */
export interface InspectorProps {
  projectId: string;
  /** The selected nodes, as the canvas has them. */
  selection: ReadonlySet<string>;
  actions: CanvasActions;
  /** Opens the editor of the text of the element of id over it on the canvas. */
  onEditText: (id: string) => void;
  onClose: () => void;
  returnFocus: () => HTMLElement | null;
}

/** The inspector of what is selected on the canvas. */
export function Inspector({
  projectId,
  selection,
  actions,
  onEditText,
  onClose,
  returnFocus,
}: InspectorProps) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
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
  const single = nodes.length === 1 ? nodes[0] : undefined;
  const title =
    single !== undefined
      ? single.title === ""
        ? t("canvas.node.untitled")
        : single.title
      : nodes.length === 0
        ? t("inspector.title")
        : t("inspector.selected", { count: nodes.length });
  return (
    <Panel
      title={title}
      subtitle={single === undefined ? undefined : typeName(t, single)}
      onClose={onClose}
      returnFocus={returnFocus}
    >
      {nodes.length === 0 ? (
        <p className="text-fg-muted">{t("inspector.empty")}</p>
      ) : (
        <div className="space-y-6">
          {single !== undefined && (
            <>
              <NodeForm key={single.id} projectId={projectId} node={single} />
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
                <Cover key={single.id} projectId={projectId} node={single} />
              )}
            </>
          )}
          {nodes.length > 1 && <Arrange nodes={nodes} actions={actions} />}
          <ColorChoice nodes={nodes} actions={actions} />
          <Tags projectId={projectId} nodes={nodes} actions={actions} />
          <section className="space-y-2">
            <h3 className="text-lg font-semibold">{t("inspector.actions")}</h3>
            <div className="flex flex-wrap gap-2">
              <Button
                onClick={() => void actions.duplicate(nodes.map((n) => n.id))}
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

/** Returns the name of a node's type, and an element's kind, as the inspector's subtitle says it. */
function typeName(
  t: ReturnType<typeof useTranslation>["t"],
  node: Node,
): string {
  switch (node.type) {
    case "branch":
      return t("canvas.node.branch");
    case "jumper":
      return t("canvas.node.jumper");
    default:
      return node.kind === "narration"
        ? t("canvas.node.narration")
        : t("canvas.node.dialogue");
  }
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

/** Returns the number that text spells, or undefined for none. */
function numberOf(text: string): number | undefined {
  const trimmed = text.trim();
  if (trimmed === "") {
    return undefined;
  }
  const n = Number(trimmed);
  return Number.isFinite(n) ? n : undefined;
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
  const [automatic, setAutomatic] = useState(shown.size === undefined);
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
    const was = valuesOf(against);
    const changed = changedOf(typed, was);
    const expected_rev = { [against.id]: against.rev };
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
      update.size = typed.automatic
        ? {}
        : { width: width as number, height: height as number };
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
        payload: {
          nodes: [
            { id: shown.id, position: { x: x as number, y: y as number } },
          ],
        },
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
        : { position: { x: x as number, y: y as number } }),
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
            onChange={(e) => setAutomatic(e.currentTarget.checked)}
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
}: {
  nodes: readonly Node[];
  actions: CanvasActions;
}) {
  const { t } = useTranslation();
  const colors = new Set(nodes.map((n) => n.color));
  // Nodes of several colours show none of them chosen.
  const value = colors.size === 1 ? [...colors][0] : "";
  return (
    <SelectField
      label={t("inspector.color")}
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
}: {
  projectId: string;
  nodes: readonly Node[];
  actions: CanvasActions;
}) {
  const { t } = useTranslation();
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
  });
  const ids = nodes.map((n) => n.id);
  const tags = tagsOf(nodes);
  const count = (tag: string) => {
    const key = tagKey(tag);
    return nodes.filter((n) => n.tags.some((x) => tagKey(x) === key)).length;
  };
  const add = (event: FormEvent) => {
    event.preventDefault();
    const tag = typed.trim();
    if (tag === "" || [...tag].length > 50) {
      setProblem(t("inspector.error.tag"));
      return;
    }
    setProblem(undefined);
    setTyped("");
    void actions.addTag(ids, tag);
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
                onClick={() => void actions.removeTag(ids, tag)}
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
            error={problem}
          />
          <datalist id={listId}>
            {(suggestions.data ?? []).map((s) => (
              <option key={s.tag} value={s.tag} />
            ))}
          </datalist>
        </div>
        <Button type="submit">{t("inspector.add")}</Button>
      </form>
    </section>
  );
}

/**
 * An element's cover image: its thumbnail, and its upload and removal, each
 * a change of its own, expecting the revision that the inspector showed as
 * the user asked for it, as a component's image is (FR-EDIT-06 AC3).
 */
function Cover({ projectId, node }: { projectId: string; node: Node }) {
  const { t } = useTranslation();
  const inputId = useId();
  const session = useQuery(sessionQuery).data;
  const doc = useProjectDocument(projectId);
  // An upload and the command that sets the cover to what it uploaded are
  // one request of the page's, which goes on to set the cover once the
  // inspector shows another node, rather than leave the asset uploaded and
  // the element without it.
  const cover = useMutation({
    mutationFn: async ({ file, rev }: { file?: File; rev: number }) => {
      const asset =
        file === undefined
          ? undefined
          : (
              await api.call("uploadAsset", {
                path: { project_id: projectId },
                headers: csrfHeaders(session),
                body: file,
              })
            ).body;
      return doc.send([
        {
          type: "node.update",
          payload: { id: node.id, cover_asset_id: asset?.id ?? "" },
          expected_rev: { [node.id]: rev },
        },
      ]);
    },
  });
  const chosen = (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.currentTarget;
    const file = input.files?.[0];
    if (file === undefined) {
      return;
    }
    cover.mutate(
      { file, rev: node.rev },
      {
        onSettled: () => {
          input.value = "";
        },
      },
    );
  };
  const refusal = refusalOf(cover.error, t);
  const title = node.title === "" ? t("canvas.node.untitled") : node.title;
  return (
    <section className="space-y-2">
      <h3 className="text-lg font-semibold">{t("inspector.cover")}</h3>
      {node.cover_asset_id === undefined ? (
        <p className="text-fg-muted">{t("inspector.no_cover")}</p>
      ) : (
        <img
          src={thumbnailOf(projectId, node.cover_asset_id)}
          alt={t("inspector.cover_alt", { node: title })}
          className="max-h-32 max-w-full rounded-md border border-border"
        />
      )}
      <Alert message={refusal?.message} />
      <div className="flex flex-wrap items-end gap-2">
        <div className="flex flex-col gap-1">
          <label htmlFor={inputId} className="font-medium">
            {node.cover_asset_id === undefined
              ? t("inspector.upload_cover")
              : t("inspector.replace_cover")}
          </label>
          <input
            id={inputId}
            type="file"
            accept={imageTypes}
            disabled={cover.isPending}
            onChange={chosen}
            className="min-h-11"
          />
        </div>
        {node.cover_asset_id !== undefined && (
          <Button
            disabled={cover.isPending}
            onClick={() => cover.mutate({ rev: node.rev })}
          >
            {t("inspector.remove_cover")}
          </Button>
        )}
      </div>
    </section>
  );
}
