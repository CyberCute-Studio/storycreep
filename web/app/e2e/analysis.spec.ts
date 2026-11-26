// The cross-check of the client-side analysis engine against the server
// (plan item 89, note N96, PRD FR-OFF-05): the small and the medium
// fixtures, which cmd/fixturegen loads through the API, as it loads them
// for the canvas's benchmark, and a project that each rule of FR-DEP-03
// finds something in, built through the command endpoint, each read
// through the API as the web app's document reads it, and analysed by the
// TypeScript engine in this process, whose validation findings, graph,
// contributions, and usages, and play mode's projection of the draft
// (item 71, N10), must be the server's: the report of GET …/validation,
// the graph of GET …/dependencies, the lists of each edge's contributions
// and of each variable's and component's usages, and the files of the
// serializer's draft export (item 76). The engine runs in Node, not in a
// page, so the desktop's run alone makes the cross-check.

import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { APIRequestContext } from "@playwright/test";
import type {
  Board,
  Component,
  ComponentType,
  Connection,
  Contribution,
  Graph,
  Node,
  ProjectBody,
  Report,
  RichTextDoc,
  Summary,
  Usage,
  Variable,
} from "@storycreep/shared/api";
import { createI18n } from "@storycreep/shared/i18n";
import { newId } from "@storycreep/shared/ids";
import { type AnalysisSources, readDraft } from "../src/analysis/draft.ts";
import {
  contributionOrder,
  contributionsOf,
  edgeKinds,
  type GraphFilter,
  graphOf,
} from "../src/analysis/graph.ts";
import {
  type ComponentUse,
  componentUsages,
  indexOf,
  usageOrder,
  variableUsages,
} from "../src/analysis/uses.ts";
import { rules, validate } from "../src/analysis/validation.ts";
import { emptyDoc } from "../src/document/doc.ts";
import { projectDraft } from "../src/play/projection.ts";
import { readZip } from "../src/releases/zip.ts";
import { makeProject } from "./boards.ts";
import { apply, type Command, firstWorkspace } from "./content.ts";
import { baseURL, binary, serverModule } from "./environment.ts";
import {
  type ApiSession,
  apiSignIn,
  expect,
  registerAccount,
  test,
} from "./fixtures.ts";

/** The server's messages in English, by key, which format the engine's findings as the server's report does. */
const serverMessages = createI18n("en", {
  catalogs: {
    en: JSON.parse(
      readFileSync(join(serverModule, "internal/i18n/catalog/en.json"), "utf8"),
    ) as Record<string, string>,
  },
});

/** The most items that a page of a list of the API holds. */
const pageSize = 200;

test.describe("the client-side analysis engine", () => {
  test.beforeEach(() => {
    test.skip(
      test.info().project.use.hasTouch === true,
      "a cross-check of the server, in Node, which the desktop's run makes",
    );
  });

  test("agrees with the server on the small fixture", async ({ request }) => {
    test.setTimeout(5 * 60_000);
    const session = await signedIn(request);
    const projectId = await loadFixture(request, session, "small", 1);
    await crossCheck(request, projectId, {
      usages: true,
      contributions: "all",
    });
  });

  test("agrees with the server on the medium fixture", async ({ request }) => {
    test.setTimeout(15 * 60_000);
    const session = await signedIn(request);
    const projectId = await loadFixture(request, session, "medium", 2);
    await crossCheck(request, projectId, { usages: false, contributions: 12 });
  });

  test("agrees with the server on a project that each rule finds something in", async ({
    request,
  }) => {
    const session = await signedIn(request);
    const projectId = await makeProject(request, session, "Every rule");
    await buildEveryRule(request, session, projectId);
    const findings = await crossCheck(request, projectId, {
      usages: true,
      contributions: "all",
    });
    // Every rule finds something, so that each is cross-checked.
    expect([...new Set(findings.map((f) => f.code))].sort()).toEqual(
      rules.map((r) => r.code).sort(),
    );
  });
});

/** Registers an account, signs the request context in as it, and returns its session. */
async function signedIn(request: APIRequestContext): Promise<ApiSession> {
  return apiSignIn(request, (await registerAccount(request, "Ana Lyst")).email);
}

/**
 * Loads the fixture of size into a project of its own, with cmd/fixturegen
 * and an API token of the account, and returns the project's ID. Its seed
 * is the test's attempt's own, since a fixture's IDs, which the seed
 * makes, are taken once it applies. fixturegen runs beside the test, which
 * can time out meanwhile, and is stopped once two thirds of the test's time
 * have gone, so that a load that stalls fails the test with fixturegen's
 * own failure, while the test has the time to report it.
 */
async function loadFixture(
  request: APIRequestContext,
  session: ApiSession,
  size: "small" | "medium",
  of: number,
): Promise<string> {
  const info = test.info();
  const seed = 89_000 + 1000 * info.repeatEachIndex + 10 * info.retry + of;
  const workspace = await firstWorkspace(request);
  const created = await request.post(
    `/api/v1/workspaces/${workspace.id}/tokens`,
    {
      headers: { "X-CSRF-Token": session.csrf },
      data: { name: "The analysis cross-check", scopes: ["project:write"] },
    },
  );
  expect(created.status(), await created.text()).toBe(201);
  const token = (await created.json()) as { secret: string };
  const projectId = await makeProject(request, session, `Fixture ${size}`);
  const dir = mkdtempSync(join(tmpdir(), "storycreep-analysis-"));
  const tokenFile = join(dir, "token");
  writeFileSync(tokenFile, `${token.secret}\n`, { mode: 0o600 });
  try {
    const started = Date.now();
    const exit = await new Promise<{
      code: number | null;
      signal: NodeJS.Signals | null;
    }>((resolve, reject) => {
      const run = spawn(
        binary("fixturegen"),
        [
          "-size",
          size,
          "-seed",
          String(seed),
          "-server",
          baseURL,
          "-project",
          projectId,
          "-token-file",
          tokenFile,
        ],
        { stdio: "inherit", timeout: Math.floor((info.timeout * 2) / 3) },
      );
      run.once("error", reject);
      run.once("exit", (code, signal) => resolve({ code, signal }));
    });
    expect(
      exit.code,
      `fixturegen -size ${size}${exit.signal === null ? "" : `, stopped by ${exit.signal}`}`,
    ).toBe(0);
    console.log(
      `The ${size} fixture loaded in ${Math.round((Date.now() - started) / 1000)} s.`,
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
  return projectId;
}

/** Returns the JSON of the answer to a GET of path, which must be 200. */
async function get<T>(
  request: APIRequestContext,
  path: string,
  headers: Record<string, string> = {},
): Promise<T> {
  const res = await request.get(path, { headers });
  expect(res.status(), `${path}: ${await res.text()}`).toBe(200);
  return (await res.json()) as T;
}

/** Returns every item of the list of path, a page at a time. */
async function every<T>(
  request: APIRequestContext,
  path: string,
  query: Record<string, string> = {},
): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  do {
    const q = new URLSearchParams({
      ...query,
      limit: String(pageSize),
      ...(cursor === undefined ? {} : { cursor }),
    });
    const page = await get<{ items: T[]; next_cursor: string | null }>(
      request,
      `${path}?${q}`,
    );
    items.push(...page.items);
    cursor = page.next_cursor ?? undefined;
  } while (cursor !== undefined);
  return items;
}

/**
 * Reads the project as the web app's document reads it (store.ts, and play
 * mode's reads of what the document lacks, session.ts): the project, its
 * boards, its variables, its component types, and its components, each of
 * them whole, and every board's nodes and connections.
 */
async function readDocument(
  request: APIRequestContext,
  projectId: string,
): Promise<AnalysisSources> {
  const at = `/api/v1/projects/${projectId}`;
  const { seq, role: _, ...project } = await get<ProjectBody>(request, at);
  const boards = await every<Board>(request, `${at}/boards`);
  const variables = await every<Variable>(request, `${at}/variables`);
  const componentTypes = await every<ComponentType>(
    request,
    `${at}/component-types`,
  );
  const summaries = await every<Summary>(request, `${at}/components`);
  const components: Component[] = [];
  for (const c of summaries) {
    components.push(await get<Component>(request, `${at}/components/${c.id}`));
  }
  const nodes: Node[] = [];
  const connections: Connection[] = [];
  for (const b of boards) {
    nodes.push(...(await every<Node>(request, `${at}/boards/${b.id}/nodes`)));
    connections.push(
      ...(await every<Connection>(request, `${at}/boards/${b.id}/connections`)),
    );
  }
  const byId = <T extends { id: string }>(items: T[]) =>
    new Map(items.map((e) => [e.id, e]));
  return {
    ...emptyDoc(seq),
    project,
    boards: byId(boards),
    nodes: byId(nodes),
    connections: byId(connections),
    variables: byId(variables),
    componentTypes: byId(componentTypes),
    components: byId(components),
  };
}

/** What crossCheck compares beyond the findings, the graph, and the projection. */
interface Checks {
  /** Whether to compare the usages of every variable and component. */
  usages: boolean;
  /** The contributions of how many edges to compare: of every edge, or of the first few. */
  contributions: "all" | number;
}

/**
 * Reads the project projectId as the web app's document does, analyses it
 * with the TypeScript engine, and expects of it what the server answers of
 * the same draft, and returns the findings.
 */
async function crossCheck(
  request: APIRequestContext,
  projectId: string,
  checks: Checks,
): Promise<Report["findings"]> {
  const at = `/api/v1/projects/${projectId}`;
  const view = await readDocument(request, projectId);
  const draft = readDraft(view);
  const index = indexOf(draft);

  // The validation report, of every finding, each with its message as the
  // server's catalog formats its arguments.
  const report = await get<Report>(request, `${at}/validation`, {
    "Accept-Language": "en",
  });
  expect(report.seq).toBe(draft.seq);
  expect(report.muted).toEqual([]);
  const findings = validate(draft, index).map(({ args, ...f }) => ({
    ...f,
    message: serverMessages.t(`validation.${f.code}`, args),
  }));
  expect(findings).toEqual(report.findings);

  // The graph, whole and as each kind of filter keeps it.
  const graph = await get<Graph>(request, `${at}/dependencies`);
  expect(graphOf(draft, index)).toEqual(graph);
  const filters: GraphFilter[] = edgeKinds.map((kind) => ({ kind }));
  for (const b of [...draft.boards.keys()].slice(0, 3)) {
    filters.push({ board: b });
  }
  for (const v of [...draft.variables.keys()].slice(0, 3)) {
    filters.push({ variable: v }, { variable: v, kind: "jumper" });
  }
  const component = [...draft.components.values()][0];
  const attribute = [...draft.attributes.values()].find(
    (a) => a.component_type_id === component?.component_type_id,
  );
  if (component !== undefined) {
    filters.push({ component: component.id });
  }
  if (attribute !== undefined) {
    filters.push(
      { attribute: attribute.id },
      { component: component?.id, attribute: attribute.id },
    );
  }
  for (const f of filters) {
    const q = new URLSearchParams({
      ...(f.kind === undefined ? {} : { kind: f.kind }),
      ...(f.board === undefined ? {} : { board_id: f.board }),
      ...(f.variable === undefined ? {} : { variable_id: f.variable }),
      ...(f.component === undefined ? {} : { component_id: f.component }),
      ...(f.attribute === undefined ? {} : { attribute_id: f.attribute }),
    });
    expect(graphOf(draft, index, f), `the graph of ${q}`).toEqual(
      await get<Graph>(request, `${at}/dependencies?${q}`),
    );
  }

  // Each edge's contributions, as many as the graph counts, and those of
  // the edges that checks names as the server lists them.
  const edges =
    checks.contributions === "all"
      ? graph.edges
      : graph.edges.slice(0, checks.contributions);
  for (const e of graph.edges) {
    expect(
      contributionsOf(draft, index, e.from_board_id, e.to_board_id, e.kind)
        ?.length,
    ).toBe(e.contributions);
  }
  for (const e of edges) {
    const path = `${at}/dependencies/${e.from_board_id}/${e.to_board_id}`;
    const listed = await every<Contribution>(request, path, { kind: e.kind });
    expect(
      contributionsOf(draft, index, e.from_board_id, e.to_board_id, e.kind),
      `the contributions of ${e.kind} of ${path}`,
    ).toEqual(listed.sort(contributionOrder));
  }

  // The usages of each variable and component.
  if (checks.usages) {
    for (const v of draft.variables.keys()) {
      for (const access of ["read", "write"] as const) {
        const listed = await every<Usage>(
          request,
          `${at}/variables/${v}/usages`,
          {
            access,
          },
        );
        expect(
          variableUsages(draft, index, v, access),
          `the ${access}s of ${v}`,
        ).toEqual(listed.sort(usageOrder));
      }
    }
    for (const c of draft.components.keys()) {
      for (const use of [
        "speaker",
        "mention",
        "read",
        "write",
      ] as ComponentUse[]) {
        const listed = await every<Usage>(
          request,
          `${at}/components/${c}/usages`,
          {
            use,
          },
        );
        expect(
          componentUsages(draft, index, c, use),
          `the ${use}s of ${c}`,
        ).toEqual(listed.sort(usageOrder));
      }
    }
  }

  // Play mode's projection of the draft, file by file, against the
  // serializer's draft export, but for the manifest's generator and the
  // moment of its snapshot.
  const res = await request.get(`${at}/draft/export`);
  expect(res.status(), await res.text()).toBe(200);
  const exported = await readZip(new Uint8Array(await res.body()));
  const projected = (await projectDraft(view)).files;
  expect([...projected.keys()].sort()).toEqual([...exported.keys()].sort());
  const text = (bytes: Uint8Array | undefined) =>
    new TextDecoder().decode(bytes);
  for (const [path, bytes] of exported) {
    if (path === "storycreep.json") {
      continue;
    }
    expect(text(projected.get(path)), path).toBe(text(bytes));
  }
  const manifest = (bytes: Uint8Array | undefined) => {
    const m = JSON.parse(text(bytes)) as {
      generator?: unknown;
      snapshot: { pulledAt?: string };
    };
    delete m.generator;
    delete m.snapshot.pulledAt;
    return m;
  };
  expect(manifest(projected.get("storycreep.json"))).toEqual(
    manifest(exported.get("storycreep.json")),
  );
  return report.findings;
}

/** A rich text of one paragraph of the inline nodes. */
function richText(...inlines: unknown[]): RichTextDoc {
  return {
    type: "doc",
    content: [{ type: "paragraph", content: inlines }],
  } as RichTextDoc;
}

/** A rich text of one paragraph of text. */
function paragraph(text: string): RichTextDoc {
  return richText({ type: "text", text });
}

/**
 * Builds in projectId a project that each rule of FR-DEP-03 finds
 * something in, as the server's own test of the report does
 * (TestValidationReport), and that has an edge of each kind between its
 * boards, a text of each kind of owner that names a variable, an attribute,
 * and a component, choices of one label whose conditions the prover of
 * duplicate choices proves exclusive, and does not, and owners of
 * expressions and texts in the trash whose UUIDs are those of entities of
 * other kinds outside it.
 */
async function buildEveryRule(
  request: APIRequestContext,
  session: ApiSession,
  projectId: string,
): Promise<void> {
  const [harbour, lighthouse, mill, nook] = [0, 1, 2, 3].map(() =>
    newId("board"),
  );
  const [seen, gold, spent, doomed, local] = [0, 1, 2, 3, 4].map(() =>
    newId("variable"),
  );
  const cast = newId("component_type");
  const [trust, secret] = [newId("attribute_def"), newId("attribute_def")];
  const [ghost, elara] = [newId("component"), newId("component")];
  const n = () => newId("node");
  const c = () => newId("connection");
  const [start, end, bad, dead, empty, talk, talk2, island] = [
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
  ];
  const [br, br0, j1, j2, j3, jd, jm, gone, a, b, nookStart] = [
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
    n(),
  ];
  const [c1, c2] = [c(), c()];
  const [o1, o2] = [newId("branch_output"), newId("branch_output")];
  // A connection whose UUID a client chose to be island's, and a node whose
  // UUID it chose to be Elara's, both deleted below.
  const twin = (prefix: "cn" | "nd", id: string) =>
    `${prefix}_${id.slice(id.indexOf("_") + 1)}`;
  const [islandTwin, elaraTwin] = [twin("cn", island), twin("nd", elara)];
  const position = { x: 0, y: 0 };
  const element = (
    id: string,
    board: string,
    text: RichTextDoc | undefined,
    more: Record<string, unknown> = {},
  ): Command => ({
    type: "node.create",
    payload: {
      id,
      board_id: board,
      type: "element",
      kind: "narration",
      position,
      ...(text === undefined ? {} : { text }),
      ...more,
    },
  });
  const typed = (
    id: string,
    board: string,
    type: "branch" | "jumper",
    more: Record<string, unknown> = {},
  ): Command => ({
    type: "node.create",
    payload: { id, board_id: board, type, position, ...more },
  });
  const connect = (
    id: string,
    from: string,
    to: string,
    label: RichTextDoc | undefined,
    more: Record<string, unknown> = {},
  ): Command => ({
    type: "connection.create",
    payload: {
      id,
      from_node_id: from,
      to_node_id: to,
      ...(label === undefined ? {} : { label }),
      ...more,
    },
  });
  const value = (ref: string, attribute?: string) => ({
    type: "valueRef",
    attrs: attribute === undefined ? { ref } : { ref, attribute },
  });
  const mention = (id: string) => ({ type: "mention", attrs: { id } });
  const text = (t: string) => ({ type: "text", text: t });
  await apply(request, session, projectId, [
    { type: "board.create", payload: { id: harbour, name: "Harbour" } },
    { type: "board.create", payload: { id: lighthouse, name: "Lighthouse" } },
    { type: "board.create", payload: { id: mill, name: "Mill" } },
    { type: "board.create", payload: { id: nook, name: "Nook" } },
    {
      type: "variable.create",
      payload: { id: seen, key: "seen", type: "bool", scope: "global" },
    },
    {
      type: "variable.create",
      payload: { id: gold, key: "gold", type: "int", scope: "global" },
    },
    {
      type: "variable.create",
      payload: { id: spent, key: "spent", type: "int", scope: "global" },
    },
    {
      type: "variable.create",
      payload: { id: doomed, key: "doomed", type: "int", scope: "global" },
    },
    {
      type: "variable.create",
      payload: {
        id: local,
        key: "local",
        type: "int",
        scope: "board",
        board_id: mill,
      },
    },
    {
      type: "component_type.create",
      payload: { id: cast, name: "Cast", key: "cast" },
    },
    {
      type: "attribute.create",
      payload: {
        id: trust,
        component_type_id: cast,
        name: "Trust",
        key: "trust",
        type: "int",
        runtime: true,
      },
    },
    {
      type: "attribute.create",
      payload: {
        id: secret,
        component_type_id: cast,
        name: "Secret",
        key: "secret",
        type: "string",
        runtime: false,
      },
    },
    {
      type: "component.create",
      payload: {
        id: ghost,
        component_type_id: cast,
        name: "Ghost",
        key: "ghost",
      },
    },
    {
      type: "component.create",
      payload: {
        id: elara,
        component_type_id: cast,
        name: "Elara",
        key: "elara",
        // A description that mentions Ghost, deleted below, and shows
        // gold: no runtime shows it, so it may show any variable.
        description: richText(text("Haunted by "), mention(ghost), value(gold)),
      },
    },
    // Harbour: start, its entry, whose text shows doomed, deleted below,
    // Mill's local, which Harbour does not read, Elara's secret, which is
    // not runtime, and Elara's trust; it leads to end, an ending, by c1,
    // whose condition reads seen and whose script writes gold and spent,
    // which nothing reads, and by c2,
    // the same choice, with the same empty label and no condition, and to
    // itself.
    element(
      start,
      harbour,
      richText(
        text("Hello "),
        value(doomed),
        value(local),
        value(elara, secret),
        value(elara, trust),
        mention(elara),
      ),
      { title: "Start" },
    ),
    element(end, harbour, paragraph("Bye"), { is_ending: true }),
    connect(c1, start, end, undefined, {
      condition: "seen",
      on_traverse: "gold = 1; spent = gold",
    }),
    connect(c2, start, end, undefined),
    connect(c(), start, start, paragraph("Again")),
    // Choices of one label whose conditions never hold together, and two
    // of another whose conditions may.
    connect(c(), start, end, paragraph("Pick"), { condition: "gold < 3" }),
    connect(c(), start, end, paragraph("Pick"), { condition: "gold >= 3" }),
    connect(c(), start, end, paragraph("Pick2"), { condition: "gold == 1" }),
    connect(c(), start, end, paragraph("Pick2"), {
      condition: "gold == 1 || seen",
    }),
    // An element whose script does not compile, one that no connection
    // leaves, reached by a choice that reads whether Mill's a was visited,
    // one without text, a line of dialogue without a speaker, one whose
    // speaker is deleted below, and an ending that nothing reaches.
    element(bad, harbour, paragraph("Bad"), { on_enter: "nope = 1" }),
    connect(c(), start, bad, paragraph("Bad")),
    connect(c(), bad, end, undefined),
    element(dead, harbour, paragraph("Dead")),
    connect(c(), start, dead, paragraph("Dead"), {
      condition: `visited(node("${a}"))`,
    }),
    element(empty, harbour, undefined, { is_ending: true }),
    connect(c(), start, empty, paragraph("Empty")),
    element(talk, harbour, paragraph("Hi"), {
      kind: "dialogue",
      is_ending: true,
    }),
    connect(c(), start, talk, paragraph("Talk")),
    element(talk2, harbour, paragraph("Boo"), {
      kind: "dialogue",
      is_ending: true,
      speaker_component_id: ghost,
    }),
    connect(c(), start, talk2, paragraph("Talk2")),
    element(island, harbour, paragraph("Island"), { is_ending: true }),
    // Island's twin, whose condition does not compile, and whose label
    // shows spent, which nothing else reads, and Elara's, whose text shows
    // doomed, deleted below: in the trash, neither is a site of the rules,
    // though island and Elara are outside it.
    connect(islandTwin, island, end, richText(value(spent)), {
      condition: "nope",
    }),
    element(elaraTwin, harbour, richText(value(doomed)), { is_ending: true }),
    // A branch whose output o2 no connection leaves, and one without
    // outputs.
    typed(br, harbour, "branch"),
    connect(c(), start, br, paragraph("Branch")),
    {
      type: "branch_output.create",
      payload: { id: o1, node_id: br, condition: "elara.trust > 2" },
    },
    {
      type: "branch_output.create",
      payload: { id: o2, node_id: br, condition: "true" },
    },
    {
      type: "connection.create",
      payload: { id: c(), from_node_id: br, from_port: o1, to_node_id: end },
    },
    typed(br0, harbour, "branch"),
    connect(c(), start, br0, paragraph("Branch0")),
    // A jumper without a target, one that leads to a jumper, and one that
    // leads to a node of Lighthouse, deleted below.
    typed(j1, harbour, "jumper"),
    connect(c(), start, j1, paragraph("J1")),
    typed(j3, harbour, "jumper", {
      target: { board_id: harbour, node_id: end },
    }),
    typed(j2, harbour, "jumper", {
      target: { board_id: harbour, node_id: j3 },
    }),
    connect(c(), start, j2, paragraph("J2")),
    element(gone, lighthouse, paragraph("Gone"), { is_ending: true }),
    typed(jd, harbour, "jumper", {
      target: { board_id: lighthouse, node_id: gone },
    }),
    connect(c(), start, jd, paragraph("JD")),
    { type: "board.set_entry_node", payload: { id: harbour, node_id: start } },
    // Mill has no entry node, and two roots: a, whose script updates
    // Elara's trust and writes its own local, and b, whose text shows gold,
    // which Harbour writes, and which a jumper of Harbour leads to.
    element(a, mill, paragraph("A"), {
      is_ending: true,
      on_enter: "elara.trust += 1\nlocal = 2",
    }),
    element(b, mill, richText(text("B "), value(gold)), { is_ending: true }),
    typed(jm, harbour, "jumper", { target: { board_id: mill, node_id: b } }),
    connect(c(), start, jm, paragraph("JM")),
    // Nook's entry node is deleted below.
    element(nookStart, nook, paragraph("Nook"), { is_ending: true }),
    { type: "board.set_entry_node", payload: { id: nook, node_id: nookStart } },
    {
      type: "project.update_settings",
      payload: { entry: { board_id: lighthouse, node_id: gone } },
    },
  ]);
  await apply(request, session, projectId, [
    { type: "connection.delete", payload: { id: islandTwin } },
    { type: "node.delete", payload: { id: elaraTwin } },
    { type: "variable.delete", payload: { id: doomed, force: true } },
    { type: "component.delete", payload: { id: ghost, force: true } },
    { type: "node.delete", payload: { id: nookStart } },
    { type: "board.delete", payload: { id: lighthouse, confirm: true } },
  ]);
}
