// What the expression editor offers to complete what is typed at the caret
// (PRD FR-EXPR-01 AC2, plan note N74): the names that are valid where the
// source is, the board's own variables, the global ones, the components and
// their runtime attributes, and the built-in functions that the source's
// context may call, each as an expression of the board writes it, with its
// type and what it is; and, within node("…"), the nodes that the page has
// read, by their titles. None is one in the trash, a variable of another
// board, an attribute that is not runtime, nor, of a condition, signal.

import {
  BUILTINS,
  builtins,
  type Context,
} from "@storycreep/shared/expr/compile";
import { valueNamer } from "../content/textNames";
import { nameMatcher } from "../ui/match";
import {
  builtinText,
  resultText,
  signatureOf,
  typeText,
  type Words,
} from "./messages";
import { boardVariables, runtimeAttributes, type ScopeSources } from "./scope";

/** What is typed at the caret that the editor offers to complete, its place in UTF-16 code units. */
export interface Spot {
  /** Where what the chosen offer replaces starts. */
  readonly from: number;
  /** Where it ends: the end of the name that the caret is in, or of the node reference's string. */
  readonly to: number;
  /** What is typed of the name, or of the node's title, before the caret. */
  readonly query: string;
  /**
   * What is completed: a name; a global variable's key after global., or a
   * board's after board.; a component's key after cmp.; an attribute's key
   * after a component's, as component says; or a node, within node("…").
   */
  readonly kind: "name" | "global" | "board" | "cmp" | "member" | "node";
  /** The key of the component of a member, and whether it is written after cmp. */
  readonly component?: { readonly key: string; readonly explicit: boolean };
}

/** What an offer is of. */
export type OfferKind =
  | "variable"
  | "attribute"
  | "component"
  | "function"
  | "node";

/** What the editor offers to write at a Spot. */
export interface Offer {
  /** What tells it from the others, whichever place in the list it has. */
  readonly id: string;
  readonly kind: OfferKind;
  /** What it shows: a name as the source writes it, a function's signature, or a node's title. */
  readonly label: string;
  /** What replaces the spot once it is chosen. */
  readonly insert: string;
  /** What it is, and of which type. */
  readonly detail: string;
  /** What its description says, of a variable, or what a function does. */
  readonly description?: string;
  /** The ID of the variable of the offer, whose sites that write it the list tells of. */
  readonly variable?: string;
}

/** The most offers that the list shows at once. */
export const MAX_OFFERS = 20;

const identifierPart = /[A-Za-z0-9_]/;

/**
 * Returns whether the end of prefix is within a string literal, and where
 * that string's quote is, or within a comment, as the lexer reads them
 * (§5.8.1): a string runs from a " to the next " that no \ escapes, or to
 * the end of its line, and a comment from // to the end of its line.
 */
function stateAt(
  prefix: string,
): { in: "code" } | { in: "comment" } | { in: "string"; quote: number } {
  let state: "code" | "string" | "comment" = "code";
  let quote = -1;
  for (let i = 0; i < prefix.length; i++) {
    const c = prefix[i];
    if (c === "\n" || c === "\r") {
      state = "code";
      continue;
    }
    if (state === "string") {
      if (c === "\\") {
        i++;
      } else if (c === '"') {
        state = "code";
      }
    } else if (state === "code") {
      if (c === '"') {
        state = "string";
        quote = i;
      } else if (c === "/" && prefix[i + 1] === "/") {
        state = "comment";
      }
    }
  }
  return state === "string"
    ? { in: "string", quote }
    : state === "comment"
      ? { in: "comment" }
      : { in: "code" };
}

/**
 * Returns what is typed at caret of source, a UTF-16 offset, that the
 * editor offers to complete, or undefined where it offers nothing: a name,
 * of a letter or an underscore and more, which may follow global., board.,
 * cmp., a component's key and a dot, or cmp., a component's key, and a dot;
 * the nothing yet typed after such a dot; or what is typed within the
 * string of node("…"). It offers nothing within another string, a comment,
 * or a number, nor where nothing is typed but after a dot.
 */
export function spotAt(source: string, caret: number): Spot | undefined {
  const prefix = source.slice(0, caret);
  const state = stateAt(prefix);
  if (state.in === "comment") {
    return undefined;
  }
  if (state.in === "string") {
    const before = prefix.slice(0, state.quote);
    const call = /(?:^|[^A-Za-z0-9_.])node\s*\(\s*$/.exec(before);
    if (call === null) {
      return undefined;
    }
    // What the chosen node replaces: the string, to its closing quote and
    // the parenthesis after it, where the source has them.
    const rest =
      /^[^"\\\n\r]*(?:"\s*\)?)?/.exec(source.slice(caret))?.[0] ?? "";
    return {
      from: state.quote,
      to: caret + rest.length,
      query: prefix.slice(state.quote + 1),
      kind: "node",
    };
  }
  const chain = /[A-Za-z0-9_.]*$/.exec(prefix)?.[0] ?? "";
  if (chain === "" || /^[0-9.]/.test(chain)) {
    return undefined;
  }
  const parts = chain.split(".");
  const word = parts[parts.length - 1];
  const qualifiers = parts.slice(0, -1);
  if (
    qualifiers.some((q) => q === "" || /^[0-9]/.test(q)) ||
    /^[0-9]/.test(word)
  ) {
    return undefined;
  }
  let after = 0;
  while (
    caret + after < source.length &&
    identifierPart.test(source[caret + after])
  ) {
    after++;
  }
  const place = { from: caret - word.length, to: caret + after, query: word };
  if (qualifiers.length === 0) {
    return word === "" ? undefined : { ...place, kind: "name" };
  }
  if (qualifiers.length === 1) {
    const [q] = qualifiers;
    if (q === "global" || q === "board" || q === "cmp") {
      return { ...place, kind: q };
    }
    return { ...place, kind: "member", component: { key: q, explicit: false } };
  }
  if (qualifiers.length === 2 && qualifiers[0] === "cmp") {
    return {
      ...place,
      kind: "member",
      component: { key: qualifiers[1], explicit: true },
    };
  }
  return undefined;
}

/** Reports whether name starts with query, whatever their case, as a key is matched as it is typed. */
function startsAs(name: string, query: string): boolean {
  return name.toLowerCase().startsWith(query.toLowerCase());
}

/** What orders the offers by what they show, made once. */
const collator = new Intl.Collator();

/**
 * Returns what the editor offers at spot, of a source of context, of the
 * board of boardId, as view has it: those that match what is typed, by
 * their keys, or by their names, as an attribute's and a component's, in
 * the order of what they show, at most MAX_OFFERS of them, and how many
 * match in all.
 */
export function offersAt(
  spot: Spot,
  context: Context,
  view: ScopeSources,
  boardId: string,
  w: Words,
): { offers: Offer[]; total: number } {
  const { t } = w;
  const name = valueNamer(view, boardId);
  // The keys that a variable that the board reads has, which name it
  // rather than a component of the key.
  const read = new Set<string>();
  const offers: Offer[] = [];
  const q = spot.query;
  const variables = boardVariables(view, boardId);
  for (const v of variables) {
    read.add(v.key);
  }
  const variableOffer = (v: (typeof variables)[number], insert: string) => ({
    id: v.id,
    kind: "variable" as const,
    label: name(v.id, undefined) ?? v.key,
    insert,
    detail:
      v.scope === "global"
        ? t("expr.offer.global_variable", { type: typeText(t, v.type) })
        : t("expr.offer.board_variable", { type: typeText(t, v.type) }),
    ...(v.description === "" ? {} : { description: v.description }),
    variable: v.id,
  });
  const components = [...view.components.values()].filter(
    (c) => c.deleted_at === undefined,
  );
  const attributesOf = (c: (typeof components)[number]) =>
    runtimeAttributes(view, c);
  const componentOffer = (
    c: (typeof components)[number],
    explicit: boolean,
  ) => {
    const written = explicit || !read.has(c.key) ? c.key : `cmp.${c.key}`;
    const type = view.componentTypes.get(c.component_type_id);
    return {
      id: c.id,
      kind: "component" as const,
      label: `${spot.kind === "cmp" ? "cmp." : ""}${written}.`,
      insert: `${spot.kind === "cmp" ? c.key : written}.`,
      detail: t("expr.offer.component", {
        component: c.name,
        type: type?.name ?? "",
      }),
    };
  };
  switch (spot.kind) {
    case "name": {
      for (const v of variables) {
        if (startsAs(v.key, q)) {
          offers.push(variableOffer(v, name(v.id, undefined) ?? v.key));
        }
      }
      for (const c of components) {
        if (startsAs(c.key, q) && attributesOf(c).length > 0) {
          offers.push(componentOffer(c, false));
        }
      }
      for (const f of builtins()) {
        const b = BUILTINS.get(f);
        if (b === undefined || (b.effect && context === "condition")) {
          continue;
        }
        if (startsAs(f, q)) {
          offers.push({
            id: `fn:${f}`,
            kind: "function",
            label: signatureOf(f),
            insert: `${f}(`,
            detail: t("expr.offer.function", { type: resultText(w, f) }),
            description: builtinText(t, f),
          });
        }
      }
      if (startsAs("node", q)) {
        offers.push({
          id: "fn:node",
          kind: "function",
          label: 'node("…")',
          insert: 'node("',
          detail: t("expr.offer.node_ref"),
          description: t("expr.builtin.node"),
        });
      }
      break;
    }
    case "global":
    case "board":
      for (const v of variables) {
        if (v.scope === spot.kind && startsAs(v.key, q)) {
          offers.push({
            ...variableOffer(v, v.key),
            label: `${spot.kind}.${v.key}`,
          });
        }
      }
      break;
    case "cmp":
      for (const c of components) {
        if (
          (startsAs(c.key, q) || nameMatcher(q)(c.name)) &&
          attributesOf(c).length > 0
        ) {
          offers.push(componentOffer(c, true));
        }
      }
      break;
    case "member": {
      const key = spot.component?.key ?? "";
      const explicit = spot.component?.explicit ?? false;
      // A variable of the key names it, rather than the component, but
      // after cmp. (§5.8.3).
      if (!explicit && read.has(key)) {
        break;
      }
      const c = components.find((x) => x.key === key);
      if (c === undefined) {
        break;
      }
      for (const a of attributesOf(c)) {
        if (startsAs(a.key, q) || (q !== "" && nameMatcher(q)(a.name))) {
          offers.push({
            id: `${c.id} ${a.id}`,
            kind: "attribute",
            label: `${explicit ? "cmp." : ""}${c.key}.${a.key}`,
            insert: a.key,
            detail: t("expr.offer.attribute", {
              attribute: a.name,
              component: c.name,
              type: typeText(t, a.type),
            }),
          });
        }
      }
      break;
    }
    case "node": {
      const matches = nameMatcher(q);
      const boards = view.boards;
      const nodes = [...view.nodes.values()].filter(
        (n) =>
          n.deleted_at === undefined &&
          n.type !== "jumper" &&
          (matches(`${n.title} ${boards.get(n.board_id)?.name ?? ""}`) ||
            (q.length >= 3 && n.id.startsWith(q))),
      );
      for (const n of nodes) {
        offers.push({
          id: n.id,
          kind: "node",
          label: n.title === "" ? t("boards.untitled_node") : n.title,
          insert: `"${n.id}")`,
          detail: t("expr.offer.node", {
            board: boards.get(n.board_id)?.name ?? t("boards.unknown_board"),
          }),
        });
      }
      // The board's own nodes first, as a reference to one of them is the
      // most often written.
      const own = (o: Offer) => view.nodes.get(o.id)?.board_id === boardId;
      offers.sort(
        (a, b) =>
          Number(own(b)) - Number(own(a)) ||
          collator.compare(a.detail, b.detail) ||
          collator.compare(a.label, b.label) ||
          (a.id < b.id ? -1 : 1),
      );
      return { offers: offers.slice(0, MAX_OFFERS), total: offers.length };
    }
  }
  offers.sort(
    (a, b) => collator.compare(a.label, b.label) || (a.id < b.id ? -1 : 1),
  );
  return { offers: offers.slice(0, MAX_OFFERS), total: offers.length };
}

/**
 * Returns the edit that writes offer at spot of source: the range that it
 * replaces, in UTF-16 code units, the text that replaces it, and where the
 * caret goes then, after what it wrote, and after the ( of a function, or
 * the dot of a component, whose name the source has a ( or a dot after
 * already, which it does not write again.
 */
export function editOf(
  source: string,
  spot: Spot,
  offer: Offer,
): { from: number; to: number; text: string; caret: number } {
  const closer =
    offer.kind === "function" ? "(" : offer.kind === "component" ? "." : "";
  if (
    closer !== "" &&
    offer.insert.endsWith(closer) &&
    source[spot.to] === closer
  ) {
    const text = offer.insert.slice(0, -1);
    return {
      from: spot.from,
      to: spot.to,
      text,
      caret: spot.from + text.length + 1,
    };
  }
  return {
    from: spot.from,
    to: spot.to,
    text: offer.insert,
    caret: spot.from + offer.insert.length,
  };
}
