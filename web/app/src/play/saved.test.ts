import type {
  Attribute,
  Board,
  ComponentType,
  Connection,
  Node,
} from "@storycreep/shared/api";
import {
  boolValue,
  intValue,
  stringValue,
} from "@storycreep/shared/expr/compile";
import { attributeKey } from "@storycreep/shared/flow";
import { describe, expect, test } from "vitest";
import { createStore } from "zustand/vanilla";
import type { ComponentEntry, Doc, Part } from "../document/doc";
import type { ProjectDocument } from "../document/store";
import { testConnection, testNode } from "../test/canvas";
import {
  expression,
  richText,
  testBoard,
  testDraft,
  testVariable,
  text,
} from "../test/play";
import { PlaySession, type SavedStart } from "./session";

// Each entity's ID, in its canonical spelling, of a mnemonic of digits and
// the letters of Crockford's base 32.
const id = (prefix: string, body: string) =>
  `${prefix}_${body.padStart(26, "0")}`;

const at = "2026-11-01T10:00:00Z";
const harbour = testBoard(id("bd", "1"), "Harbour", {
  entry_node_id: id("nd", "A"),
});
const pier = testBoard(id("bd", "2"), "Pier", {
  entry_node_id: id("nd", "P"),
});
const met = testVariable(id("var", "1"), "met", "bool", false);
const gold = testVariable(id("var", "2"), "gold", "int", 3);
const trust: Attribute = {
  id: id("atr", "1"),
  component_type_id: id("ct", "1"),
  key: "trust",
  name: "Trust",
  type: "int",
  default: 0,
  runtime: true,
  order: "a",
  rev: 1,
  created_at: at,
  updated_at: at,
};
const character: ComponentType = {
  id: id("ct", "1"),
  project_id: harbour.project_id,
  key: "character",
  name: "Character",
  icon: "person",
  attributes: [trust],
  rev: 1,
  created_at: at,
  updated_at: at,
};
const elara: ComponentEntry = {
  id: id("cmp", "1"),
  project_id: harbour.project_id,
  component_type_id: character.id,
  key: "elara",
  name: "Elara",
  order: "a",
  attributes: { [trust.id]: 1 },
  description: richText(),
  rev: 1,
  created_at: at,
  updated_at: at,
};

const on = (board: Board) => ({ board_id: board.id });

// The harbour's entry, from which Wait goes to the dock, whose on-enter
// adds to the gold and to Elara's trust, and from which Back goes back.
const nodes: Node[] = [
  testNode(id("nd", "A"), 0, 0, {
    ...on(harbour),
    title: "Harbour",
    text: richText(text("At the harbour.")),
  }),
  testNode(id("nd", "B"), 200, 0, {
    ...on(harbour),
    title: "Dock",
    text: richText(text("At the dock.")),
    on_enter: expression("gold += 2\nelara.trust += 1"),
  }),
  testNode(id("nd", "P"), 0, 0, {
    ...on(pier),
    title: "Pier",
    text: richText(text("On the pier.")),
  }),
];
const connections: Connection[] = [
  testConnection(id("cn", "1"), id("nd", "A"), id("nd", "B"), {
    ...on(harbour),
    order: "a",
    label: richText(text("Wait")),
  }),
  testConnection(id("cn", "2"), id("nd", "B"), id("nd", "A"), {
    ...on(harbour),
    order: "a",
    label: richText(text("Back")),
  }),
  testConnection(id("cn", "3"), id("nd", "P"), id("nd", "P"), {
    ...on(pier),
    order: "a",
    label: richText(text("Stay")),
  }),
];

const draft = testDraft({
  boards: [harbour, pier],
  nodes,
  connections,
  variables: [met, gold],
  componentTypes: [character],
  components: [elara],
});

// A project's document of view, which has read every part that play asks
// for.
function documentOf(view: Doc) {
  const store = createStore(() => ({ view }));
  const reads: string[] = [];
  const doc = {
    store,
    hasRead: (part: Part) => reads.includes(part),
    load: async (part: Part) => {
      reads.push(part);
    },
    readComponent: async (c: string) => {
      reads.push(c);
    },
  } as unknown as ProjectDocument;
  return { doc, store };
}

function playing(s: PlaySession) {
  const state = s.state;
  if (state.status !== "playing") {
    throw new Error(`play is ${state.status}`);
  }
  return state;
}

// savedAtDock plays the draft to the dock, sets met by hand there, and
// returns what play saves of it, as the server gives it back: through JSON.
async function savedAtDock(): Promise<SavedStart> {
  const { doc } = documentOf(draft);
  const s = new PlaySession(doc);
  await s.start({ board: harbour.id, node: "" });
  s.choose(id("cn", "1"));
  expect(s.edit(met.id, boolValue(true))).toBe(true);
  const saving = s.saving();
  if (saving === undefined) {
    throw new Error("play saves nothing");
  }
  expect(saving.names).toEqual({
    variables: { [met.id]: "met", [gold.id]: "gold" },
    attributes: { [elara.id]: { [trust.id]: "elara.trust" } },
  });
  const { state, names } = JSON.parse(JSON.stringify(saving));
  return { id: id("pst", "1"), name: "At the dock", state, names };
}

describe("a saved play state", () => {
  // A state saved at the dock, after a value set by hand, loads as it was
  // saved, in a session that was elsewhere: every value, the visit counts,
  // and the node where the flow was, whose choices loading builds again,
  // as the start's step, which Reset loads again.
  // Verifies: FR-PLAY-05/AC1
  test("sets every value and puts the flow at the node where it was saved", async () => {
    const saved = await savedAtDock();
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: pier.id, node: "" });
    s.choose(id("cn", "3"));
    const turn = playing(s).turn;
    expect(s.load(saved)).toBe(true);
    const check = () => {
      const { flow } = playing(s);
      expect(flow).toMatchObject({
        board: harbour.id,
        node: id("nd", "B"),
        phase: "awaiting_choice",
        text: "At the dock.",
        choices: [{ id: id("cn", "2"), text: "Back" }],
        halted: false,
      });
      expect(flow.values.get(met.id)).toEqual(boolValue(true));
      expect(flow.values.get(gold.id)).toEqual(intValue(5));
      expect(flow.values.get(attributeKey(elara.id, trust.id))).toEqual(
        intValue(2),
      );
      expect(flow.history).toHaveLength(1);
      expect(flow.history[0].loaded).toEqual({
        name: "At the dock",
        report: { dropped: [], droppedVisits: 0, defaulted: [], retyped: [] },
      });
      expect(flow.history[0].entered).toEqual([
        { board: harbour.id, node: id("nd", "B") },
      ]);
      expect(s.saving()?.state.visits).toEqual(saved.state.visits);
    };
    check();
    expect(playing(s).turn).toBe(turn + 1);
    expect(playing(s).start.saved).toBe(saved);
    // Play goes on from it, and Reset loads it again, as the session's
    // start.
    s.choose(id("cn", "2"));
    expect(playing(s).flow.node).toBe(id("nd", "A"));
    s.reset();
    check();
  });

  // The draft changed since the state was saved: met was deleted, gold
  // retyped, rumours added, Elara's trust made no runtime attribute, and
  // the dock deleted. The state loads all the same, with a report of what
  // loading dropped, defaulted, and could not keep, and the flow at the
  // entry of the dock's board, the harbour.
  // Verifies: FR-PLAY-05/AC2
  test("loads a state of entities deleted since, with a report of what it dropped", async () => {
    const saved = await savedAtDock();
    const rumours = testVariable(id("var", "3"), "rumours", "int", 7);
    const changed = testDraft({
      boards: [harbour, pier],
      nodes: nodes.filter((n) => n.id !== id("nd", "B")),
      connections: connections.filter((c) => c.id === id("cn", "3")),
      variables: [
        { ...gold, type: "string", default: "rich" },
        rumours,
      ] as (typeof met)[],
      componentTypes: [
        { ...character, attributes: [{ ...trust, runtime: false }] },
      ],
      components: [elara],
    });
    const { doc } = documentOf(changed);
    const s = new PlaySession(doc);
    await s.start({ board: pier.id, node: "" });
    expect(s.load(saved)).toBe(true);
    const { flow } = playing(s);
    expect(flow.history[0].loaded?.report).toEqual({
      dropped: [
        { key: met.id, name: "met" },
        { key: attributeKey(elara.id, trust.id), name: "elara.trust" },
      ],
      droppedVisits: 1,
      defaulted: [rumours.id],
      retyped: [gold.id],
      cursor: { at: "board", board: harbour.id },
    });
    // Play started at the harbour's entry, with the values that it kept,
    // and those of the draft of what it could not.
    expect(flow).toMatchObject({
      board: harbour.id,
      node: id("nd", "A"),
      text: "At the harbour.",
      errors: [],
    });
    expect(flow.values.get(gold.id)).toEqual(stringValue("rich"));
    expect(flow.values.get(rumours.id)).toEqual(intValue(7));
    // The harbour was entered before the save, and once more as play
    // started at its entry.
    expect(s.saving()?.state.visits).toEqual({ [id("nd", "A")]: 2 });
  });

  // A cursor whose board is gone too starts play at the project's entry,
  // and, where the project has none, nowhere.
  test("starts at the project's entry, or nowhere, where the cursor's board is gone", async () => {
    const saved = await savedAtDock();
    const pierOnly = {
      boards: [pier],
      nodes: nodes.filter((n) => n.board_id === pier.id),
      connections: connections.filter((c) => c.board_id === pier.id),
      variables: [met, gold],
      componentTypes: [character],
      components: [elara],
    };
    for (const [entry, cursor, node] of [
      [
        { board_id: pier.id, node_id: id("nd", "P") },
        { at: "project" },
        id("nd", "P"),
      ],
      [undefined, { at: "none" }, ""],
    ] as const) {
      const { doc } = documentOf(
        testDraft({ ...pierOnly, ...(entry === undefined ? {} : { entry }) }),
      );
      const s = new PlaySession(doc);
      await s.start({ board: pier.id, node: "" });
      expect(s.load(saved)).toBe(true);
      const { flow } = playing(s);
      expect(flow.history[0].loaded?.report.cursor).toEqual(cursor);
      expect(flow.node).toBe(node);
      expect(flow.values.get(met.id)).toEqual(boolValue(true));
    }
  });

  // A cursor at a node that can no longer await a choice, as one that no
  // connection leaves any more, is no cursor that the flow can rest at.
  test("starts at the board's entry where the cursor's node can no longer await a choice", async () => {
    const saved = await savedAtDock();
    const { doc } = documentOf(
      testDraft({
        boards: [harbour, pier],
        nodes,
        connections: connections.filter((c) => c.id !== id("cn", "2")),
        variables: [met, gold],
        componentTypes: [character],
        components: [elara],
      }),
    );
    const s = new PlaySession(doc);
    await s.start({ board: pier.id, node: "" });
    expect(s.load(saved)).toBe(true);
    const { flow } = playing(s);
    expect(flow.history[0].loaded?.report).toEqual({
      dropped: [],
      droppedVisits: 0,
      defaulted: [],
      retyped: [],
      cursor: { at: "board", board: harbour.id },
    });
    expect(flow.node).toBe(id("nd", "A"));
  });

  // A state that the runtime does not load, as one of a newer version,
  // changes nothing of the session.
  test("leaves the session as it was where the state does not load", async () => {
    const saved = await savedAtDock();
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: pier.id, node: "" });
    const before = playing(s);
    expect(() =>
      s.load({
        ...saved,
        state: { ...saved.state, stateVersion: 2 as 1 },
      }),
    ).toThrow();
    expect(s.state).toBe(before);
    s.choose(id("cn", "3"));
    expect(playing(s).flow.history).toHaveLength(2);
  });
});
