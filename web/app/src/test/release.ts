// What the tests of a project's releases read as a release's files: a zip,
// written as the server's export writes one, of each member deflated, and
// the files of a small release of the published format.

import { newId } from "@storycreep/shared/ids";

/** The IDs of the release's boards, as the address of its page takes them. */
export const harbourId = newId("board");
export const towerId = newId("board");

/** Returns data compressed with raw Deflate. */
async function deflate(data: Uint8Array<ArrayBuffer>): Promise<Uint8Array> {
  const body = new Response(data).body;
  if (body === null) {
    throw new Error("deflate: no body");
  }
  const out = body.pipeThrough(new CompressionStream("deflate-raw"));
  return new Uint8Array(await new Response(out).arrayBuffer());
}

/** Returns a zip of files, by their paths, each deflated, or stored where stored names it. */
export async function zipOf(
  files: ReadonlyMap<string, Uint8Array<ArrayBuffer>>,
  stored: ReadonlySet<string> = new Set(),
): Promise<Uint8Array<ArrayBuffer>> {
  const encoder = new TextEncoder();
  const parts: Uint8Array[] = [];
  const directory: Uint8Array[] = [];
  let offset = 0;
  for (const [path, data] of files) {
    const name = encoder.encode(path);
    const packed = stored.has(path) ? data : await deflate(data);
    const method = stored.has(path) ? 0 : 8;
    const local = new Uint8Array(30 + name.length);
    const l = new DataView(local.buffer);
    l.setUint32(0, 0x04034b50, true);
    l.setUint16(8, method, true);
    l.setUint32(18, packed.length, true);
    l.setUint32(22, data.length, true);
    l.setUint16(26, name.length, true);
    local.set(name, 30);
    const entry = new Uint8Array(46 + name.length);
    const e = new DataView(entry.buffer);
    e.setUint32(0, 0x02014b50, true);
    e.setUint16(10, method, true);
    e.setUint32(20, packed.length, true);
    e.setUint32(24, data.length, true);
    e.setUint16(28, name.length, true);
    e.setUint32(42, offset, true);
    entry.set(name, 46);
    parts.push(local, packed);
    directory.push(entry);
    offset += local.length + packed.length;
  }
  const directoryLength = directory.reduce((n, d) => n + d.length, 0);
  const end = new Uint8Array(22);
  const v = new DataView(end.buffer);
  v.setUint32(0, 0x06054b50, true);
  v.setUint16(8, files.size, true);
  v.setUint16(10, files.size, true);
  v.setUint32(12, directoryLength, true);
  v.setUint32(16, offset, true);
  const out = new Uint8Array(offset + directoryLength + end.length);
  let at = 0;
  for (const part of [...parts, ...directory, end]) {
    out.set(part, at);
    at += part.length;
  }
  return out;
}

const text = (value: string) => ({
  preview: value,
  segments: [{ kind: "text", marks: [], value }],
});

const condition = (source: string) => ({
  source,
  ast: { kind: "lit", type: "bool", value: true },
});

/**
 * Returns the files of a release of the board Harbour, whose entry Greet
 * leads, by the choices Ask, then Leave, to Reply and to Gate, a branch
 * whose outputs lead on, and which holds Lost, which nothing reaches, and a
 * jumper to the board Tower.
 */
export function releaseFiles(): Map<string, Uint8Array<ArrayBuffer>> {
  const harbour = {
    schemaVersion: 1,
    board: {
      id: harbourId,
      name: "Harbour",
      slug: "harbour",
      entryNodeId: "nd_a",
    },
    nodes: [
      {
        id: "nd_a",
        type: "element",
        kind: "dialogue",
        title: "Greet",
        speaker: "cmp_elara",
        isEnding: false,
        position: { x: 0, y: 0 },
        text: text("Welcome to the harbour."),
        onEnter: {
          source: "gold = 1",
          ast: { kind: "script", statements: [] },
        },
      },
      {
        id: "nd_b",
        type: "element",
        kind: "narration",
        title: "Reply",
        isEnding: true,
        position: { x: 1, y: 0 },
        text: text("The keeper nods."),
      },
      {
        id: "nd_c",
        type: "branch",
        title: "Gate",
        position: { x: 2, y: 0 },
        outputs: [
          { id: "bo_else", kind: "else", order: 1 },
          {
            id: "bo_if",
            kind: "if",
            order: 0,
            condition: condition("gold > 1"),
          },
        ],
      },
      {
        id: "nd_d",
        type: "element",
        kind: "narration",
        title: "Lost",
        isEnding: false,
        position: { x: 3, y: 0 },
      },
      {
        id: "nd_e",
        type: "jumper",
        title: "To the tower",
        position: { x: 4, y: 0 },
        target: { boardId: towerId, nodeId: "nd_t" },
      },
    ],
    connections: [
      {
        id: "cn_leave",
        from: "nd_a",
        fromPort: "out",
        to: "nd_c",
        order: 1,
        label: {
          segments: [{ kind: "text", marks: [], value: "Leave" }],
        },
      },
      {
        id: "cn_ask",
        from: "nd_a",
        fromPort: "out",
        to: "nd_b",
        order: 0,
        condition: condition("gold > 0"),
        label: {
          segments: [{ kind: "text", marks: ["bold"], value: "Ask" }],
        },
      },
      { id: "cn_if", from: "nd_c", fromPort: "bo_if", to: "nd_e", order: 0 },
    ],
  };
  const tower = {
    schemaVersion: 1,
    board: { id: towerId, name: "Tower", slug: "tower", entryNodeId: "nd_t" },
    nodes: [
      {
        id: "nd_t",
        type: "element",
        kind: "narration",
        title: "Summit",
        isEnding: true,
        position: { x: 0, y: 0 },
        text: text("The wind howls."),
      },
    ],
    connections: [],
  };
  const manifest = {
    schemaVersion: 1,
    boards: [
      {
        id: harbourId,
        name: "Harbour",
        slug: "harbour",
        file: "boards/harbour.json",
        entryNodeId: "nd_a",
      },
      {
        id: towerId,
        name: "Tower",
        slug: "tower",
        file: "boards/tower.json",
        entryNodeId: "nd_t",
      },
    ],
  };
  const variables = {
    schemaVersion: 1,
    variables: [
      { id: "var_gold", key: "gold", type: "int", scope: "global", default: 3 },
      {
        id: "var_seen",
        key: "seen",
        type: "bool",
        scope: "board",
        boardId: towerId,
        default: false,
        description: "Whether the summit was seen.",
      },
    ],
  };
  const types = {
    schemaVersion: 1,
    componentTypes: [
      {
        id: "ct_person",
        key: "person",
        name: "Person",
        attributes: [
          {
            id: "atr_mood",
            key: "mood",
            name: "Mood",
            type: "string",
            default: "",
            runtime: false,
          },
        ],
      },
    ],
  };
  const components = {
    schemaVersion: 1,
    components: [
      {
        id: "cmp_elara",
        key: "elara",
        name: "Elara",
        componentTypeId: "ct_person",
        attributes: { atr_mood: "Wary of strangers" },
      },
    ],
  };
  const encoder = new TextEncoder();
  const file = (value: unknown) =>
    encoder.encode(JSON.stringify(value)) as Uint8Array<ArrayBuffer>;
  return new Map([
    ["storycreep.json", file(manifest)],
    ["boards/harbour.json", file(harbour)],
    ["boards/tower.json", file(tower)],
    ["project/variables.json", file(variables)],
    ["project/component_types.json", file(types)],
    ["project/components.json", file(components)],
  ]);
}
