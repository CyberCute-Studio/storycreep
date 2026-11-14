import type {
  Attribute,
  ComponentType,
  Connection,
  Node,
} from "@storycreep/shared/api";
import { describe, expect, test, vi } from "vitest";
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
import { draftChanged, PlaySession, parallelReads } from "./session";

// What a test has the shared flow's load do as play loads the projection,
// before it loads it.
const hooks = vi.hoisted(() => ({
  loading: undefined as (() => void) | undefined,
}));
vi.mock("@storycreep/shared/flow", async (importOriginal) => {
  const flow = await importOriginal<typeof import("@storycreep/shared/flow")>();
  return {
    ...flow,
    load: (...args: Parameters<typeof flow.load>) => {
      hooks.loading?.();
      return flow.load(...args);
    },
  };
});

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

  // A component that the document does not hold whole once play has read
  // it, as one whose values a read of the components' list took again, is
  // no draft that play runs with its type's defaults for its values, but a
  // read that failed, which Try again makes anew.
  test("fails to start as a read where a component is not held whole, rather than play its defaults", async () => {
    const partial: ComponentEntry = { ...elaraComponent };
    delete partial.attributes;
    const { doc } = documentOf({
      ...draft,
      components: new Map([[partial.id, partial]]),
    });
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
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

  test("says the draft changed where it changed while play loaded its projection", async () => {
    const { doc, store } = documentOf(draft);
    const s = new PlaySession(doc);
    const a = draft.nodes.get(id("nd", "A")) as Node;
    hooks.loading = () => {
      hooks.loading = undefined;
      store.setState({
        view: {
          ...draft,
          nodes: new Map(draft.nodes).set(a.id, {
            ...a,
            text: richText(text("Changed.")),
          }),
        },
      });
    };
    try {
      await s.start({ board: harbour.id, node: "" });
    } finally {
      hooks.loading = undefined;
    }
    expect(playing(s).changed).toBe(true);
    expect(playing(s).flow.text).toBe("Trust 1.");
  });

  // Of a branch whose if and else both lead to one node, the path is of
  // the connection that the flow took, the else's, as the if's condition
  // is false.
  test("marks the connection that a branch took, of two that lead to one node", async () => {
    const branch = id("nd", "B");
    const output = (o: string, kind: "if" | "else", extra: object = {}) => ({
      id: id("bo", o),
      node_id: branch,
      kind,
      rev: 1,
      created_at: at,
      updated_at: at,
      ...extra,
    });
    const { doc } = documentOf(
      testDraft({
        boards: [harbour],
        variables: [gold],
        nodes: [
          testNode(id("nd", "A"), 0, 0, on(harbour)),
          {
            ...testNode(branch, 200, 0, on(harbour)),
            type: "branch",
            outputs: [
              output("1", "if", {
                order: "a",
                condition: expression("gold > 10"),
              }),
              output("2", "else"),
            ],
          } as Node,
          testNode(id("nd", "C"), 400, 0, on(harbour)),
        ],
        connections: [
          testConnection(id("cn", "1"), id("nd", "A"), branch, on(harbour)),
          testConnection(id("cn", "2"), branch, id("nd", "C"), {
            ...on(harbour),
            from_port: id("bo", "1"),
          }),
          testConnection(id("cn", "3"), branch, id("nd", "C"), {
            ...on(harbour),
            from_port: id("bo", "2"),
          }),
        ],
      }),
    );
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    s.choose(id("cn", "1"));
    const { flow } = playing(s);
    expect([...flow.visited]).toEqual([id("nd", "A"), branch, id("nd", "C")]);
    expect([...flow.traversed]).toEqual([id("cn", "1"), id("cn", "3")]);
  });

  test("reads no more once a read has failed", async () => {
    const boards = Array.from({ length: 12 }, (_, i) =>
      testBoard(id("bd", `${i + 10}`), `Board ${i}`),
    );
    const { doc, reads } = documentOf(testDraft({ boards }));
    (doc as { load: unknown }).load = async (part: Part) => {
      reads.push(part);
      if (part === `board:${boards[0].id}`) {
        throw new Error("offline");
      }
      await new Promise((resolve) => setTimeout(resolve, 0));
    };
    const s = new PlaySession(doc);
    await s.start({ board: boards[0].id, node: "" });
    expect(s.state).toMatchObject({ status: "failed", reading: true });
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(reads.filter((r) => r.startsWith("board:")).length).toBe(
      parallelReads,
    );
  });

  // A component that a read of the components' list took the values of
  // while play read the others is read whole again, as many at a time as
  // the others.
  test("reads again, parallelReads at a time, the components whose values a read of the list took", async () => {
    const components = Array.from({ length: 10 }, (_, i) => {
      const c: ComponentEntry = {
        ...elaraComponent,
        id: id("cmp", `${i + 10}`),
        key: `c${i}`,
      };
      delete c.attributes;
      return c;
    });
    const { doc, reads } = documentOf(
      testDraft({ boards: [harbour], componentTypes: [character], components }),
    );
    let reading = 0;
    let most = 0;
    (doc as { readComponent: unknown }).readComponent = async (c: string) => {
      reads.push(c);
      reading++;
      most = Math.max(most, reading);
      await new Promise((resolve) => setTimeout(resolve, 0));
      reading--;
    };
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    expect(reads.filter((r) => r.startsWith("cmp_"))).toHaveLength(20);
    expect(most).toBe(parallelReads);
  });

  // Each turn is shown once of each kind, by the board's page that shows
  // it first, and not again by a page that the user opens later.
  test("tells that a turn is shown first once of each kind", async () => {
    const { doc } = documentOf(draft);
    const s = new PlaySession(doc);
    await s.start({ board: harbour.id, node: "" });
    const { turn } = playing(s);
    expect(s.firstShown("canvas", turn)).toBe(true);
    expect(s.firstShown("canvas", turn)).toBe(false);
    expect(s.firstShown("panel", turn)).toBe(true);
    s.choose(id("cn", "3"));
    expect(s.firstShown("canvas", turn)).toBe(false);
    expect(s.firstShown("canvas", playing(s).turn)).toBe(true);
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

test("the server's answer to the page's own change of an expression, compiled, and a branch output's revision, are no change of the draft", () => {
  const a = draft.nodes.get(id("nd", "A")) as Node;
  const b = draft.nodes.get(id("nd", "B")) as Node;
  const answered = {
    ...draft,
    nodes: new Map(draft.nodes)
      .set(a.id, {
        ...a,
        rev: 2,
        on_exit: {
          source: "elara.trust += 1",
          is_valid: true,
          ast: { kind: "script", statements: [] },
          ast_version: 1,
        },
      })
      .set(b.id, {
        ...b,
        outputs: (b.outputs ?? []).map((o) => ({
          ...o,
          rev: 2,
          updated_at: "2026-11-02T10:00:00Z",
        })),
      }),
  };
  expect(draftChanged(draft, answered)).toBe(false);
  expect(
    draftChanged(draft, {
      ...answered,
      nodes: new Map(answered.nodes).set(a.id, {
        ...a,
        on_exit: expression("elara.trust += 2"),
      }),
    }),
  ).toBe(true);
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
