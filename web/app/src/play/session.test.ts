import type {
  Attribute,
  ComponentType,
  Connection,
  Node,
} from "@storycreep/shared/api";
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
import { draftChanged, PlaySession } from "./session";

// Each entity's ID, in its canonical spelling, of a mnemonic of digits and
// the letters of Crockford's base 32.
const id = (prefix: string, body: string) =>
  `${prefix}_${body.padStart(26, "0")}`;

const harbour = testBoard(id("bd", "1"), "Harbour", {
  entry_node_id: id("nd", "A"),
});
const elara = testBoard(id("bd", "2"), "Elara", {
  entry_node_id: id("nd", "E"),
});
const met = testVariable(id("var", "1"), "met", "bool", false);
const gold = testVariable(id("var", "2"), "gold", "int", 3);
// Of Elara's board: one that keeps its value, and one reset on entering it.
const chats = testVariable(id("var", "3"), "chats", "int", 0, {
  scope: "board",
  board_id: elara.id,
});
const mood = testVariable(id("var", "4"), "mood", "int", 0, {
  scope: "board",
  board_id: elara.id,
  reset_on_enter: true,
});
const at = "2026-11-01T10:00:00Z";
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
const elaraComponent: ComponentEntry = {
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

const on = (board: { id: string }) => ({ board_id: board.id });
const value = (ref: string, attribute?: string) => ({
  type: "valueRef" as const,
  attrs: attribute === undefined ? { ref } : { ref, attribute },
});

const nodes: Node[] = [
  // The harbour's entry, whose on-exit raises Elara's trust (FR-COMP-03 AC1).
  testNode(id("nd", "A"), 0, 0, {
    ...on(harbour),
    kind: "dialogue",
    title: "Harbour",
    speaker_component_id: elaraComponent.id,
    text: richText(
      text("Trust "),
      value(elaraComponent.id, trust.id),
      text("."),
    ),
    on_exit: expression("elara.trust += 1"),
  }),
  {
    ...testNode(id("nd", "J"), 200, 0, on(harbour)),
    type: "jumper",
    target: { board_id: elara.id, node_id: id("nd", "E") },
  } as Node,
  // A branch whose one output's condition is false, and that has no else.
  {
    ...testNode(id("nd", "B"), 200, 200, on(harbour)),
    type: "branch",
    outputs: [
      {
        id: id("bo", "1"),
        node_id: id("nd", "B"),
        kind: "if",
        order: "a",
        condition: expression("gold > 10"),
        rev: 1,
        created_at: at,
        updated_at: at,
      },
    ],
  } as Node,
  // A node that nothing leads to.
  testNode(id("nd", "Z"), 400, 400, {
    ...on(harbour),
    title: "Nowhere",
    text: richText(text("Unreached.")),
  }),
  testNode(id("nd", "E"), 0, 0, {
    ...on(elara),
    title: "Elara",
    text: richText(
      text("Chats "),
      value(chats.id),
      text(", mood "),
      value(mood.id),
      text(", trust "),
      value(elaraComponent.id, trust.id),
      text("."),
    ),
    on_enter: expression("chats += 1\nmood += 1"),
  }),
  {
    ...testNode(id("nd", "K"), 200, 0, on(elara)),
    type: "jumper",
    target: { board_id: harbour.id, node_id: id("nd", "A") },
  } as Node,
];

// The harbour's choices, in the order of their keys, which is neither the
// order of their IDs nor of their creation (FR-EDIT-03 AC1).
const connections: Connection[] = [
  testConnection(id("cn", "3"), id("nd", "A"), id("nd", "J"), {
    ...on(harbour),
    order: "a",
    label: richText(text("Visit Elara")),
  }),
  testConnection(id("cn", "1"), id("nd", "A"), id("nd", "B"), {
    ...on(harbour),
    order: "b",
    label: richText(text("Wait")),
  }),
  testConnection(id("cn", "2"), id("nd", "A"), id("nd", "B"), {
    ...on(harbour),
    order: "c",
    label: richText(text("Secret")),
    condition: expression("met"),
  }),
  testConnection(id("cn", "4"), id("nd", "E"), id("nd", "K"), {
    ...on(elara),
    label: richText(text("Back")),
  }),
];

const draft = testDraft({
  boards: [harbour, elara],
  nodes,
  connections,
  variables: [met, gold, chats, mood],
  componentTypes: [character],
  components: [elaraComponent],
});

// A project's document of view, which counts the reads that play asks of
// it, of parts and of components whole.
function documentOf(view: Doc) {
  const store = createStore(() => ({ view }));
  const reads: string[] = [];
  const doc = {
    store,
    hasRead: (_: Part) => false,
    load: async (part: Part) => {
      reads.push(part);
    },
    readComponent: async (c: string) => {
      reads.push(c);
    },
  } as unknown as ProjectDocument;
  return { doc, store, reads };
}

function playing(s: PlaySession) {
  const state = s.state;
  if (state.status !== "playing") {
    throw new Error(`play is ${state.status}`);
  }
  return state;
}

describe("a play session", () => {
  // Starting reads what the document does not hold, once; no step reads
  // anything of the server, as the flow runs on the projected snapshot.
  // Verifies: FR-PLAY-01/AC1
  test("runs entirely in the page, with no read for a step", async () => {
    const { doc, reads } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    expect(reads.sort()).toEqual(
      [
        "tree",
        "variables",
        "components",
        `board:${harbour.id}`,
        `board:${elara.id}`,
      ].sort(),
    );
    const before = reads.length;
    s.choose(id("cn", "3"));
    s.choose(id("cn", "4"));
    s.choose(id("cn", "1"));
    expect(reads).toHaveLength(before);
    expect(playing(s).flow.steps).toBe(4);
  });

  // Verifies: FR-PLAY-01/AC2
  test("starts at a node that no path from an entry node reaches", async () => {
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: id("nd", "Z") });
    const { flow } = playing(s);
    expect(flow.node).toBe(id("nd", "Z"));
    expect(flow.text).toBe("Unreached.");
    expect(flow.errors).toEqual([]);
  });

  // A jumper to another board moves the flow there, which the board's page
  // follows (the session's board), with the values that it had: Elara's
  // trust, raised on the harbour, shows on Elara's board.
  // Verifies: FR-PLAY-01/AC3, FR-EDIT-05/AC3, FR-COMP-03/AC1
  test("follows a jumper to another board and keeps the state", async () => {
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    expect(playing(s).flow).toMatchObject({
      board: harbour.id,
      node: id("nd", "A"),
      text: "Trust 1.",
      speaker: "Elara",
    });
    const turn = playing(s).turn;
    s.choose(id("cn", "3"));
    const { flow } = playing(s);
    expect(flow.board).toBe(elara.id);
    expect(flow.node).toBe(id("nd", "E"));
    expect(flow.text).toBe("Chats 1, mood 1, trust 2.");
    expect(playing(s).turn).toBe(turn + 1);
    // The canvas highlights the path: the nodes entered, and the
    // connection taken, which the jumper's target was entered by.
    expect([...flow.visited]).toEqual([id("nd", "A"), id("nd", "E")]);
    expect([...flow.traversed]).toEqual([id("cn", "3")]);
  });

  // A board's variable keeps its value as the flow enters its board again,
  // and one with reset_on_enter is reset to its default first.
  // Verifies: FR-VAR-02/AC3, FR-VAR-02/AC4
  test("keeps a board's variable across its board's entries, and resets one that says so", async () => {
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    s.choose(id("cn", "3"));
    s.choose(id("cn", "4"));
    expect(playing(s).flow).toMatchObject({
      board: harbour.id,
      text: "Trust 2.",
    });
    s.choose(id("cn", "3"));
    expect(playing(s).flow.text).toBe("Chats 2, mood 1, trust 3.");
  });

  // Verifies: FR-EDIT-03/AC1
  test("lists the choices in their author's order, without one whose condition is false", async () => {
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    expect(playing(s).flow.choices).toEqual([
      { id: id("cn", "3"), text: "Visit Elara" },
      { id: id("cn", "1"), text: "Wait" },
    ]);
  });

  // Verifies: FR-EDIT-04/AC1
  test("ends at a branch without an else whose conditions are all false, as a dead end", async () => {
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    s.choose(id("cn", "1"));
    const { flow } = playing(s);
    expect(flow.node).toBe(id("nd", "B"));
    expect(flow.phase).toBe("ended");
    expect(flow.ended).toBe("dead_end");
    expect(flow.choices).toEqual([]);
  });

  test("reports a runtime error with where it is and its source, and halts there", async () => {
    const divides = testDraft({
      boards: [harbour],
      variables: [gold],
      nodes: [
        testNode(id("nd", "A"), 0, 0, {
          ...on(harbour),
          on_enter: expression("gold = gold / 0"),
        }),
        testNode(id("nd", "B"), 0, 0, on(harbour)),
      ],
      connections: [
        testConnection(
          id("cn", "1"),
          id("nd", "A"),
          id("nd", "B"),
          on(harbour),
        ),
      ],
    });
    const { doc } = documentOf(divides);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: id("nd", "A") });
    const { flow } = playing(s);
    expect(flow.errors).toEqual([
      {
        code: "division_by_zero",
        at: id("nd", "A"),
        expression: "onEnter",
        board: harbour.id,
        node: id("nd", "A"),
        source: "gold = gold / 0",
        step: 0,
      },
    ]);
    expect(flow.halted).toBe(true);
  });

  test("reads the boards and the components whole that the document does not hold, and fails to start where a read fails", async () => {
    const partial: ComponentEntry = { ...elaraComponent };
    delete partial.attributes;
    const view = { ...draft, components: new Map([[partial.id, partial]]) };
    const { doc, store, reads } = documentOf(view);
    (doc as { readComponent: unknown }).readComponent = async (c: string) => {
      reads.push(c);
      store.setState({
        view: {
          ...store.getState().view,
          components: new Map([[c, elaraComponent]]),
        },
      });
    };
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    expect(reads).toContain(elaraComponent.id);
    expect(playing(s).flow.text).toBe("Trust 1.");
    (doc as { load: unknown }).load = async () => {
      throw new Error("offline");
    };
    await s.restart();
    expect(s.state).toMatchObject({ status: "failed", reading: true });
  });

  test("fails to run a draft that holds what no project does, saying it is no failed read", async () => {
    const twin = { ...elaraComponent, id: id("cmp", "2") };
    const { doc } = documentOf({
      ...draft,
      components: new Map([
        [elaraComponent.id, elaraComponent],
        [twin.id, twin],
      ]),
    });
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    expect(s.state).toMatchObject({ status: "failed", reading: false });
  });

  test("says once the draft changed since it started, and restarts on the draft as it is", async () => {
    const { doc, store } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    // A read again of the same, at a new revision, changes nothing.
    const a = draft.nodes.get(id("nd", "A")) as Node;
    store.setState({
      view: {
        ...draft,
        nodes: new Map(draft.nodes).set(a.id, { ...a, rev: 2 }),
      },
    });
    expect(playing(s).changed).toBe(false);
    const changed = {
      ...draft,
      nodes: new Map(draft.nodes).set(a.id, {
        ...a,
        text: richText(text("Changed.")),
      }),
    };
    store.setState({ view: changed });
    expect(playing(s).changed).toBe(true);
    await s.restart();
    expect(playing(s)).toMatchObject({ changed: false });
    expect(playing(s).flow.text).toBe("Changed.");
  });

  test("stops, and does nothing more of a start that a stop overtook", async () => {
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    const started = s.start({ board: harbour.id, node: "" });
    s.stop();
    await started;
    expect(s.state.status).toBe("idle");
  });
});

test("a component's values that a read of the list leaves out are no change of the draft", () => {
  const partial: ComponentEntry = { ...elaraComponent, rev: 2 };
  delete partial.attributes;
  delete partial.description;
  const after = {
    ...draft,
    components: new Map([[partial.id, partial]]),
  };
  expect(draftChanged(draft, after)).toBe(false);
  expect(
    draftChanged(draft, {
      ...after,
      components: new Map([[partial.id, { ...partial, name: "Mira" }]]),
    }),
  ).toBe(true);
});
