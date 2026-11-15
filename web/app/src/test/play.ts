// What play mode's tests build drafts of (plan item 71): the project's
// document of a published snapshot, as the API would answer the draft that
// the snapshot was written of, which play mode's projection writes back
// (play/projection.test.ts), and entities of a draft, as the API answers
// them.

import type {
  Attribute,
  Board,
  BranchOutput,
  ComponentType,
  Connection,
  Expression,
  Node,
  RichTextDoc,
  RichTextInline,
  RichTextMarks,
  Variable,
} from "@storycreep/shared/api";
import type {
  Board as BoardFile,
  Components,
  ComponentTypes,
  Manifest,
  Segment,
  Settings,
  Variables,
} from "@storycreep/shared/published";
import { type ComponentEntry, type Doc, emptyDoc } from "../document/doc";

const at = "2026-11-01T10:00:00Z";

/** The ID of the project of testDraft, in its canonical spelling, as a snapshot names it. */
export const testProjectId = "prj_0000000000000000000000TEST";

/** Returns an expression of source, as the API answers one that the page has not had compiled. */
export function expression(source: string): Expression {
  return { source, is_valid: true };
}

/** Returns a rich text of one paragraph of inline nodes, or of none. */
export function richText(...inlines: RichTextInline[]): RichTextDoc {
  return {
    type: "doc",
    content: [
      inlines.length === 0
        ? { type: "paragraph" }
        : { type: "paragraph", content: inlines },
    ],
  };
}

/** Returns a text node of a rich text. */
export function text(value: string): RichTextInline {
  return { type: "text", text: value };
}

/** Returns the key of order of the place i, which orders as the places do. */
export function orderKey(i: number): string {
  return `k${String(i).padStart(4, "0")}`;
}

/** Returns a board of the project, as the API answers it. */
export function testBoard(
  id: string,
  name: string,
  extra: Partial<Board> = {},
): Board {
  return {
    id,
    project_id: testProjectId,
    name,
    slug: name.toLowerCase().replace(/[^a-z0-9]+/g, "-"),
    description: "",
    color: "none",
    order: id,
    rev: 1,
    created_at: at,
    updated_at: at,
    ...extra,
  };
}

/** Returns a variable of the project, as the API answers it. */
export function testVariable(
  id: string,
  key: string,
  type: Variable["type"],
  value: Variable["default"],
  extra: Partial<Variable> = {},
): Variable {
  return {
    id,
    project_id: testProjectId,
    key,
    type,
    default: value,
    description: "",
    scope: "global",
    reset_on_enter: false,
    order: id,
    rev: 1,
    created_at: at,
    updated_at: at,
    ...extra,
  };
}

/** Returns a document of what entities holds, its project prj_test, read whole. */
export function testDraft(entities: {
  boards?: Board[];
  nodes?: Node[];
  connections?: Connection[];
  variables?: Variable[];
  componentTypes?: ComponentType[];
  components?: ComponentEntry[];
  entry?: { board_id?: string; node_id?: string };
}): Doc {
  const byId = <T extends { id: string }>(list: T[] = []) =>
    new Map(list.map((e) => [e.id, e]));
  return {
    ...emptyDoc(1),
    project: {
      id: testProjectId,
      workspace_id: "ws_test",
      name: "Test",
      slug: "test",
      description: "",
      default_locale: "en",
      entry: entities.entry ?? {},
      publish_min_role: "editor",
      rev: 1,
      created_at: at,
      updated_at: at,
    },
    boards: byId(entities.boards),
    nodes: byId(entities.nodes),
    connections: byId(entities.connections),
    variables: byId(entities.variables),
    componentTypes: byId(entities.componentTypes),
    components: byId(entities.components),
  };
}

// richTextOf returns the rich text of segments, a document of paragraphs,
// which is all that the conformance suite's texts hold.
function richTextOf(segments: readonly Segment[] | undefined): RichTextDoc {
  const content: RichTextDoc["content"] = [];
  let inlines: RichTextInline[] = [];
  const end = () => {
    content.push(
      inlines.length === 0
        ? { type: "paragraph" }
        : { type: "paragraph", content: inlines },
    );
    inlines = [];
  };
  for (const s of segments ?? []) {
    if (
      s.kind === "item" ||
      (s.kind === "paragraph" && s.depth !== undefined)
    ) {
      throw new Error("richTextOf: a list, which no suite's text holds");
    }
    if (s.kind === "paragraph") {
      end();
      continue;
    }
    if (s.kind === "break") {
      inlines.push({ type: "hardBreak" });
      continue;
    }
    const marks: RichTextMarks = s.marks.map((m) =>
      m === "link"
        ? { type: "link", attrs: { href: s.href ?? "" } }
        : { type: m },
    );
    const marked = marks.length === 0 ? {} : { marks };
    if (s.kind === "text") {
      inlines.push({ type: "text", text: s.value, ...marked });
    } else if (s.kind === "mention") {
      inlines.push({ type: "mention", attrs: { id: s.ref }, ...marked });
    } else {
      const decimals = s.decimals === undefined ? {} : { decimals: s.decimals };
      inlines.push({
        type: "valueRef",
        attrs:
          s.attribute === undefined
            ? { ref: s.ref, ...decimals }
            : { ref: s.ref, attribute: s.attribute, ...decimals },
        ...marked,
      });
    }
  }
  end();
  return { type: "doc", content };
}

/**
 * Returns the draft that the snapshot of files was written of, as the
 * project's document holds it, read whole: each entity as the API answers
 * it, each expression by its source alone, which the projection compiles,
 * and each text a rich text of its segments.
 */
export function draftOf(files: ReadonlyMap<string, Uint8Array>): Doc {
  const read = <T>(path: string): T => {
    const bytes = files.get(path);
    if (bytes === undefined) {
      throw new Error(`draftOf: there is no ${path}`);
    }
    return JSON.parse(new TextDecoder().decode(bytes)) as T;
  };
  const manifest = read<Manifest>("storycreep.json");
  const settings = read<Settings>("project/settings.json");
  const source = (e: { source: string } | undefined) =>
    e === undefined ? {} : { source: e.source, is_valid: true };
  const boards: Board[] = [];
  const nodes: Node[] = [];
  const connections: Connection[] = [];
  for (const b of manifest.boards) {
    const f = read<BoardFile>(b.file);
    boards.push(
      testBoard(b.id, b.name, {
        slug: b.slug,
        ...(b.entryNodeId === undefined
          ? {}
          : { entry_node_id: b.entryNodeId }),
      }),
    );
    for (const n of f.nodes) {
      const base = {
        id: n.id,
        board_id: b.id,
        project_id: manifest.project.id,
        type: n.type,
        title: n.title ?? "",
        color: "none" as const,
        tags: n.type === "element" ? (n.tags ?? []) : [],
        is_ending: n.type === "element" && n.isEnding,
        position: n.position,
        rev: 1,
        created_at: at,
        updated_at: at,
      };
      if (n.type === "element") {
        nodes.push({
          ...base,
          kind: n.kind,
          text: richTextOf(n.text?.segments),
          ...(n.speaker === undefined
            ? {}
            : { speaker_component_id: n.speaker }),
          ...(n.cover === undefined ? {} : { cover_asset_id: n.cover }),
          ...(n.onEnter === undefined ? {} : { on_enter: source(n.onEnter) }),
          ...(n.onExit === undefined ? {} : { on_exit: source(n.onExit) }),
        } as Node);
      } else if (n.type === "branch") {
        nodes.push({
          ...base,
          outputs: n.outputs.map(
            (o) =>
              ({
                id: o.id,
                node_id: n.id,
                kind: o.kind,
                ...(o.kind === "else" ? {} : { order: orderKey(o.order) }),
                ...(o.kind === "else"
                  ? {}
                  : { condition: source(o.condition) }),
                rev: 1,
                created_at: at,
                updated_at: at,
              }) as BranchOutput,
          ),
          ...(n.onEnter === undefined ? {} : { on_enter: source(n.onEnter) }),
          ...(n.onExit === undefined ? {} : { on_exit: source(n.onExit) }),
        } as Node);
      } else {
        nodes.push({
          ...base,
          ...(n.target === undefined
            ? {}
            : {
                target: {
                  board_id: n.target.boardId,
                  node_id: n.target.nodeId,
                },
              }),
        } as Node);
      }
    }
    for (const c of f.connections) {
      connections.push({
        id: c.id,
        board_id: b.id,
        project_id: manifest.project.id,
        from_node_id: c.from,
        from_port: c.fromPort,
        to_node_id: c.to,
        order: orderKey(c.order),
        label: richTextOf(c.label?.segments),
        ...(c.condition === undefined
          ? {}
          : { condition: source(c.condition) }),
        ...(c.onTraverse === undefined
          ? {}
          : { on_traverse: source(c.onTraverse) }),
        rev: 1,
        created_at: at,
        updated_at: at,
      } as Connection);
    }
  }
  const variables = read<Variables>("project/variables.json").variables.map(
    (v, i) =>
      testVariable(v.id, v.key, v.type, v.default, {
        project_id: manifest.project.id,
        description: v.description ?? "",
        scope: v.scope,
        order: orderKey(i),
        ...(v.scope === "board"
          ? { board_id: v.boardId, reset_on_enter: v.resetOnEnter === true }
          : {}),
      }),
  );
  const componentTypes = read<ComponentTypes>(
    "project/component_types.json",
  ).componentTypes.map(
    (t): ComponentType => ({
      id: t.id,
      project_id: manifest.project.id,
      key: t.key,
      name: t.name,
      icon: "none",
      attributes: t.attributes.map(
        (a, i) =>
          ({
            id: a.id,
            component_type_id: t.id,
            key: a.key,
            name: a.name,
            type: a.type,
            default: a.default,
            runtime: a.runtime,
            ...(a.type === "enum" ? { options: a.options } : {}),
            order: orderKey(i),
            rev: 1,
            created_at: at,
            updated_at: at,
          }) as Attribute,
      ),
      rev: 1,
      created_at: at,
      updated_at: at,
    }),
  );
  const components = read<Components>("project/components.json").components.map(
    (c, i): ComponentEntry => ({
      id: c.id,
      project_id: manifest.project.id,
      component_type_id: c.componentTypeId,
      key: c.key,
      name: c.name,
      ...(c.image === undefined ? {} : { image_asset_id: c.image }),
      attributes: c.attributes,
      description: { type: "doc", content: [{ type: "paragraph" }] },
      order: orderKey(i),
      rev: 1,
      created_at: at,
      updated_at: at,
    }),
  );
  const doc = testDraft({
    boards,
    nodes,
    connections,
    variables,
    componentTypes,
    components,
    entry:
      settings.entry === undefined
        ? {}
        : { board_id: settings.entry.boardId, node_id: settings.entry.nodeId },
  });
  return {
    ...doc,
    project: {
      ...(doc.project as NonNullable<Doc["project"]>),
      id: manifest.project.id,
      name: manifest.project.name,
      slug: manifest.project.slug,
    },
  };
}
