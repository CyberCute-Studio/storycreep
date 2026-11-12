// What the canvas's accessibility tree holds (PRD NFR-A11Y-03, plan note
// N70): the board's nodes, in the order that a reader reads the board in,
// from its top to its bottom, and across where two start at the same
// height, each named by what its card shows, its title, the line of its
// type, and its text's opening, a branch's outputs, or a jumper's target,
// and under each node the connections that leave it, in the order of its
// ports and of each port's choices, each named by the node that it enters,
// its label, and, of a branch's, the output that it leaves. The canvas's
// pixels tell none of it to assistive technology; the tree (CanvasTree.tsx) is
// what does.

import type { Connection, Node } from "@storycreep/shared/api";
import type { Doc } from "../document/doc";
import { choicesOfPort } from "./flow";
import {
  type LabelContext,
  type NodeLabels,
  outputLabel,
  plainText,
  titleOf,
} from "./labels";
import { portsOf } from "./layout";
import type { NodeItem, Scene } from "./scene";

/** The messages that the tree names its items with. */
export type TreeKey =
  | "canvas.tree.node"
  | "canvas.tree.node_body"
  | "canvas.tree.connection"
  | "canvas.tree.connection_labelled"
  | "canvas.tree.output_connection"
  | "canvas.tree.output_connection_labelled";

/** The page's messages, as the tree and the labels of cards read them. */
export type TreeMessages = (
  key: TreeKey | Parameters<LabelContext["t"]>[0],
  args?: Record<string, string>,
) => string;

/** A node of the board, as the tree lists it. */
export interface TreeRow {
  id: string;
  /** What names it: what its card shows. */
  label: string;
  /** Its title, as its card shows it, which typing finds it by. */
  title: string;
  /** How many connections leave it, which are its children. */
  leaving: number;
}

/** A connection that leaves a node, as the tree lists it under the node. */
export interface TreeChoice {
  id: string;
  label: string;
}

/** Returns text on one line, its lines apart by a semicolon, as a name is read. */
function oneLine(text: string): string {
  return text
    .split("\n")
    .map((line) => line.replace(/\s+/g, " ").trim())
    .filter((line) => line !== "")
    .join("; ");
}

/** Returns what names a node whose card shows labels: its title, the line of its type, and what its card's body says, on one line. */
export function nodeLabel(labels: NodeLabels, t: TreeMessages): string {
  const body = oneLine(labels.body);
  return body === ""
    ? t("canvas.tree.node", { title: labels.title, kind: labels.kind })
    : t("canvas.tree.node_body", {
        title: labels.title,
        kind: labels.kind,
        body,
      });
}

/**
 * Returns what returns the rows of the tree of scene: its nodes, in the
 * order that a reader reads the board in, each named by what describe
 * returns of it, with how many connections leave it. A node's name is made
 * once for each state of the node, as an entity that did not change is the
 * same object, and made anew for all of them once labelling, which counts
 * the changes of the names that cards show of other things, as of a
 * speaker, or t, the page's messages, is another, so that a change of one
 * node of a board of thousands names one node anew.
 */
export function treeRows(): (
  scene: Scene,
  describe: (item: NodeItem) => NodeLabels,
  labelling: number,
  t: TreeMessages,
) => TreeRow[] {
  let names = new WeakMap<Node, { label: string; title: string }>();
  let made: { labelling: number; t: TreeMessages } | undefined;
  return (scene, describe, labelling, t) => {
    if (made === undefined || made.labelling !== labelling || made.t !== t) {
      names = new WeakMap();
      made = { labelling, t };
    }
    const leaving = new Map<string, number>();
    scene.connections.forEach((_, item) => {
      const from = item.connection.from_node_id;
      leaving.set(from, (leaving.get(from) ?? 0) + 1);
    });
    const items: NodeItem[] = [];
    scene.nodes.forEach((_, item) => {
      items.push(item);
    });
    items.sort(
      (a, b) =>
        a.rect.y - b.rect.y ||
        a.rect.x - b.rect.x ||
        (a.node.id < b.node.id ? -1 : a.node.id > b.node.id ? 1 : 0),
    );
    return items.map((item) => {
      let name = names.get(item.node);
      if (name === undefined) {
        const labels = describe(item);
        name = { label: nodeLabel(labels, t), title: labels.title };
        names.set(item.node, name);
      }
      return {
        id: item.node.id,
        label: name.label,
        title: name.title,
        leaving: leaving.get(item.node.id) ?? 0,
      };
    });
  };
}

/**
 * Returns the connections that leave the node of id, of those that scene
 * draws, in the order of the node's ports, an element's one, or a branch's
 * outputs, and of each port's choices (FR-EDIT-03 AC1), each named by the
 * node that it enters, as that node's card titles it, by its label, as
 * plain words, where it has one, and, of a branch's, by the row of the
 * output that it leaves, as the branch's card says it.
 */
export function treeChoices(
  scene: Scene,
  view: Pick<Doc, "components" | "variables">,
  id: string,
  t: TreeMessages,
): TreeChoice[] {
  const node = scene.nodes.get(id)?.node;
  if (node === undefined) {
    return [];
  }
  const leaving: Connection[] = [];
  scene.connections.forEach((_, item) => {
    if (item.connection.from_node_id === id) {
      leaving.push(item.connection);
    }
  });
  const names = {
    componentName: (component: string) => view.components.get(component)?.name,
    variableKey: (variable: string) => view.variables.get(variable)?.key,
  };
  const choices: TreeChoice[] = [];
  for (const port of portsOf(node)) {
    const output =
      node.type === "branch"
        ? node.outputs?.find((o) => o.id === port)
        : undefined;
    for (const c of choicesOfPort(leaving, port)) {
      const to = scene.nodes.get(c.to_node_id)?.node;
      const target = to === undefined ? c.to_node_id : titleOf(to, t);
      const label = oneLine(plainText(c.label, names));
      choices.push({
        id: c.id,
        label:
          output === undefined
            ? label === ""
              ? t("canvas.tree.connection", { target })
              : t("canvas.tree.connection_labelled", { target, label })
            : label === ""
              ? t("canvas.tree.output_connection", {
                  output: outputLabel(output, t),
                  target,
                })
              : t("canvas.tree.output_connection_labelled", {
                  output: outputLabel(output, t),
                  target,
                  label,
                }),
      });
    }
  }
  return choices;
}

/**
 * Returns the place in rows of the row that typing typed finds from the
 * place from: the next after it whose title starts with typed, whatever
 * its case, around the list's end to its start, or, of what is typed on
 * from one letter to the next, the row at from itself where it still
 * starts so; none where no title starts so.
 */
export function typedRow(
  rows: readonly Pick<TreeRow, "title">[],
  from: number,
  typed: string,
): number | undefined {
  const prefix = typed.toLocaleLowerCase();
  if (prefix === "" || rows.length === 0) {
    return undefined;
  }
  const starts = (i: number) =>
    rows[i].title.toLocaleLowerCase().startsWith(prefix);
  // What goes on from the letters before it stays on the row that they
  // found, while it still starts so; one letter moves on to the next.
  const first = typed.length > 1 ? from : from + 1;
  for (let n = 0; n < rows.length; n++) {
    const i = (((first + n) % rows.length) + rows.length) % rows.length;
    if (starts(i)) {
      return i;
    }
  }
  return undefined;
}
