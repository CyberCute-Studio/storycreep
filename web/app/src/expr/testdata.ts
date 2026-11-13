// What the expression editor's tests read a board's names from: a project
// of two boards, Harbour and Lighthouse, with global variables, Harbour's
// own, one of which has a global one's key, and Lighthouse's, which
// Harbour's expressions may not read, a component with a runtime
// attribute, one that is not runtime, and one of options, and nodes.

import type {
  Board,
  ComponentType,
  Node,
  Variable,
} from "@storycreep/shared/api";
import { createI18n } from "@storycreep/shared/i18n";
import type { ComponentEntry } from "../document/doc";
import type { Words } from "./messages";
import { type Scope, type ScopeSources, scopeOf } from "./scope";

const at = "2026-11-01T10:00:00Z";

export const ids = {
  harbour: "bd_01HQ8Z3XK4F7NAWR5TCQ9M2BA1",
  lighthouse: "bd_01HQ8Z3XK4F7NAWR5TCQ9M2BA2",
  metElara: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB1",
  gold: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB2",
  trustGlobal: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB3",
  trustBoard: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB4",
  keeper: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB5",
  name: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB6",
  lamp: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB7",
  binned: "var_01HQ8Z3XK4F7NAWR5TCQ9M2BB8",
  elara: "cmp_01HQ8Z3XK4F7NAWR5TCQ9M2BC1",
  character: "ct_01HQ8Z3XK4F7NAWR5TCQ9M2BD1",
  trust: "atr_01HQ8Z3XK4F7NAWR5TCQ9M2BE1",
  notes: "atr_01HQ8Z3XK4F7NAWR5TCQ9M2BE2",
  mood: "atr_01HQ8Z3XK4F7NAWR5TCQ9M2BE3",
  quay: "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BF1",
  tower: "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BF2",
  onward: "nd_01HQ8Z3XK4F7NAWR5TCQ9M2BF3",
} as const;

/** Returns a variable of the test project. */
export function variable(
  id: string,
  key: string,
  more: Partial<Variable> = {},
): Variable {
  return {
    id,
    project_id: "prj_01HQ8Z3XK4F7NAWR5TCQ9M2BZ1",
    key,
    type: "int",
    default: 0,
    scope: "global",
    reset_on_enter: false,
    description: "",
    order: "V",
    rev: 1,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

/** Returns a node of the test project. */
export function node(
  id: string,
  board: string,
  title: string,
  more: Partial<Node> = {},
): Node {
  return {
    id,
    board_id: board,
    project_id: "prj_01HQ8Z3XK4F7NAWR5TCQ9M2BZ1",
    type: "element",
    kind: "narration",
    title,
    color: "none",
    tags: [],
    is_ending: false,
    position: { x: 0, y: 0 },
    text: { type: "doc", content: [{ type: "paragraph" }] },
    rev: 1,
    created_at: at,
    updated_at: at,
    ...more,
  };
}

function board(id: string, name: string): Board {
  return {
    id,
    project_id: "prj_01HQ8Z3XK4F7NAWR5TCQ9M2BZ1",
    name,
    slug: name.toLowerCase(),
    description: "",
    color: "none",
    order: "V",
    rev: 1,
    created_at: at,
    updated_at: at,
  };
}

export const character: ComponentType = {
  id: ids.character,
  project_id: "prj_01HQ8Z3XK4F7NAWR5TCQ9M2BZ1",
  key: "character",
  name: "Character",
  icon: "person",
  attributes: [
    {
      id: ids.trust,
      component_type_id: ids.character,
      key: "trust",
      name: "Trust",
      type: "int",
      default: 0,
      runtime: true,
      order: "1",
      rev: 1,
      created_at: at,
      updated_at: at,
    },
    {
      id: ids.notes,
      component_type_id: ids.character,
      key: "notes",
      name: "Notes",
      type: "text",
      default: "",
      runtime: false,
      order: "2",
      rev: 1,
      created_at: at,
      updated_at: at,
    },
    {
      id: ids.mood,
      component_type_id: ids.character,
      key: "mood",
      name: "Mood",
      type: "enum",
      options: ["calm", "angry"],
      default: "calm",
      runtime: true,
      order: "3",
      rev: 1,
      created_at: at,
      updated_at: at,
    },
  ],
  rev: 1,
  created_at: at,
  updated_at: at,
};

export const elara: ComponentEntry = {
  id: ids.elara,
  project_id: "prj_01HQ8Z3XK4F7NAWR5TCQ9M2BZ1",
  component_type_id: ids.character,
  key: "elara",
  name: "Elara",
  order: "V",
  rev: 1,
  created_at: at,
  updated_at: at,
};

/** The variables of the test project. */
export const variables: Variable[] = [
  variable(ids.metElara, "met_elara", {
    type: "bool",
    description: "Whether the player has met Elara.",
  }),
  variable(ids.gold, "gold"),
  variable(ids.trustGlobal, "trust", { type: "float" }),
  variable(ids.trustBoard, "trust", {
    scope: "board",
    board_id: ids.harbour,
  }),
  variable(ids.keeper, "keeper", { type: "string" }),
  variable(ids.name, "name", { type: "string" }),
  variable(ids.lamp, "lamp", { scope: "board", board_id: ids.lighthouse }),
  variable(ids.binned, "binned", { deleted_at: at }),
];

/** Returns what the names of the test project's expressions are read from. */
export function testSources(more: Partial<ScopeSources> = {}): ScopeSources {
  return {
    variables: new Map(variables.map((v) => [v.id, v])),
    components: new Map([[elara.id, elara]]),
    componentTypes: new Map([[character.id, character]]),
    boards: new Map([
      [ids.harbour, board(ids.harbour, "Harbour")],
      [ids.lighthouse, board(ids.lighthouse, "Lighthouse")],
    ]),
    nodes: new Map([
      [ids.quay, node(ids.quay, ids.harbour, "Quay")],
      [ids.tower, node(ids.tower, ids.lighthouse, "Tower")],
      [
        ids.onward,
        node(ids.onward, ids.harbour, "Onward", { type: "jumper" }),
      ],
    ]),
    gone: new Map(),
    ...more,
  };
}

/** Returns the Scope of Harbour's expressions, or of another board. */
export function harbourScope(
  boardId: string = ids.harbour,
  more: Partial<ScopeSources> = {},
): Scope {
  return scopeOf(testSources(more), boardId);
}

/** Returns the editor's words in English, as the app's catalog has them. */
export function englishWords(): Words {
  const i18n = createI18n("en");
  return { t: i18n.t.bind(i18n), locale: "en" };
}
