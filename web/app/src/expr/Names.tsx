// What the expression editor says of a source below its field (PRD
// FR-EXPR-01, FR-VAR-02 AC1, FR-DEP-01 AC3, plan note N74): what a compiled
// source says in plain language (plain.ts); each name that it resolves,
// with what it resolved to, of which scope, as a board's variable, of a
// bare key that a global one has too, and its usages, which a press shows,
// and its panel, which a link opens; each node that it refers to, by its
// title and board; and, of a name that names nothing, an offer to create
// the variable that it would be (unknown.ts).

import { walk } from "@storycreep/shared/expr/compile";
import { newId } from "@storycreep/shared/ids";
import { Link } from "@tanstack/react-router";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import {
  nodeTitleText,
  type ValueNamer,
  valueNamer,
} from "../content/textNames";
import {
  componentUsagesQuery,
  type UsageListOf,
  UsageLists,
  variableUsagesQuery,
} from "../content/usages";
import { type Command, useCommands } from "../project/queries";
import { focusIsNowhere } from "../shell/focus";
import { Button } from "../ui/Button";
import { SelectField } from "../ui/Field";
import { textLink } from "../ui/Page";
import type { Analysis } from "./analyze";
import { typeText, type Words } from "./messages";
import { type Part, plainOf } from "./plain";
import type { Scope } from "./scope";
import { unknownOf, type VariableType } from "./unknown";

/** Shows the parts of a rendering: words as they are, and names as code. */
function Parts({ parts }: { parts: readonly Part[] }) {
  return (
    <>
      {parts.map((part, i) =>
        part.kind === "code" ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: the parts of one phrase, in their order, which change together.
          <code key={i} className="rounded bg-surface px-1 font-mono">
            {part.text}
          </code>
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: as above.
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}

/** What a compiled source says in plain language: a condition's phrase, or a sentence for each of a script's statements. */
export function PlainText({
  analysis,
  scope,
  words,
}: {
  analysis: Analysis;
  scope: Scope;
  words: Words;
}) {
  const { t } = words;
  const sentences = useMemo(
    () =>
      analysis.ast === undefined
        ? []
        : plainOf(analysis.ast, words, scope.view, scope.boardId),
    [analysis.ast, words, scope],
  );
  if (sentences.length === 0) {
    return null;
  }
  return (
    <div className="space-y-1 text-sm" data-plain="">
      <p className="font-medium">{t("expr.plain.heading")}</p>
      {analysis.context === "script" ? (
        <ol className="list-decimal space-y-1 ps-5">
          {sentences.map((parts, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: a script's statements, in their order.
            <li key={i} className="break-words">
              <Parts parts={parts} />
            </li>
          ))}
        </ol>
      ) : (
        <p className="break-words">
          <Parts parts={sentences[0]} />
        </p>
      )}
    </div>
  );
}

/** A name that a compiled source resolves: a variable, a component's attribute, or a node. */
type Resolved =
  | { kind: "variable"; key: string; id: string }
  | { kind: "attribute"; key: string; component: string; attribute: string }
  | { kind: "node"; key: string; id: string };

/** Returns the names that analysis's AST resolves, each once, in the order that the source first names them. */
function resolvedOf(analysis: Analysis): Resolved[] {
  const out: Resolved[] = [];
  const seen = new Set<string>();
  const add = (r: Resolved) => {
    if (!seen.has(r.key)) {
      seen.add(r.key);
      out.push(r);
    }
  };
  if (analysis.ast === undefined) {
    return out;
  }
  walk(analysis.ast, (n) => {
    switch (n.kind) {
      case "var":
        add({ kind: "variable", key: n.ref, id: n.ref });
        break;
      case "attr":
        add({
          kind: "attribute",
          key: `${n.ref} ${n.attribute}`,
          component: n.ref,
          attribute: n.attribute,
        });
        break;
      case "node_ref":
        add({ kind: "node", key: n.ref, id: n.ref });
    }
  });
  return out;
}

/** One name that the source resolves, with what it names, as name writes it, its usages, which its button shows, and the link to its panel. */
function NameItem({
  projectId,
  item,
  scope,
  name,
  words,
}: {
  projectId: string;
  item: Resolved;
  scope: Scope;
  name: ValueNamer;
  words: Words;
}) {
  const { t } = words;
  const [shown, setShown] = useState(false);
  const regionId = useId();
  const { view } = scope;
  if (item.kind === "node") {
    // A node of a board that the page has not read is said to be one, as
    // it has no title to show.
    const node = view.nodes.get(item.id);
    return (
      <li className="break-words">
        {node === undefined
          ? t("expr.names.node_unread")
          : t("expr.names.node", {
              node: nodeTitleText(t, node.title),
              board:
                view.boards.get(node.board_id)?.name ??
                t("boards.unknown_board"),
            })}
      </li>
    );
  }
  let written: string;
  let detail: string;
  let lists: UsageListOf[];
  let panel:
    | { to: "/projects/$projectId/variables"; search: { variable: string } }
    | { to: "/projects/$projectId/components"; search: { component: string } };
  if (item.kind === "variable") {
    const variable = view.variables.get(item.id);
    written = name(item.id, undefined) ?? variable?.key ?? "";
    const type = typeText(t, variable?.type ?? "");
    detail =
      variable?.scope === "board"
        ? t("expr.names.board_variable", {
            type,
            board:
              view.boards.get(variable.board_id ?? "")?.name ??
              t("boards.unknown_board"),
          })
        : t("expr.names.global_variable", { type });
    lists = [
      {
        key: "read",
        title: t("variable.reads"),
        empty: t("variable.reads_empty"),
        query: variableUsagesQuery(projectId, item.id, "read"),
      },
      {
        key: "write",
        title: t("variable.writes"),
        empty: t("variable.writes_empty"),
        query: variableUsagesQuery(projectId, item.id, "write"),
      },
    ];
    panel = {
      to: "/projects/$projectId/variables",
      search: { variable: item.id },
    };
  } else {
    const component = view.components.get(item.component);
    const attribute =
      component === undefined
        ? undefined
        : view.componentTypes
            .get(component.component_type_id)
            ?.attributes.find((a) => a.id === item.attribute);
    written = name(item.component, item.attribute) ?? "";
    detail = t("expr.names.attribute", {
      attribute: attribute?.name ?? "",
      component: component?.name ?? "",
      type: typeText(t, attribute?.type ?? ""),
    });
    // The component's usages of the attribute alone, as the server lists
    // each site once for each attribute of it that the site reads or
    // writes.
    lists = (["read", "write"] as const).map((use) => ({
      key: use,
      title: t("expr.names.attribute_usages", { use }),
      empty: t("expr.names.attribute_usages_empty", { use }),
      query: componentUsagesQuery(projectId, item.component, use),
      only: (usage) => usage.attribute_id === item.attribute,
    }));
    panel = {
      to: "/projects/$projectId/components",
      search: { component: item.component },
    };
  }
  return (
    <li className="space-y-2" data-name={item.key}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="min-w-0 break-words">
          <code className="rounded bg-surface px-1 font-mono">{written}</code>{" "}
          <span data-resolved="">{detail}</span>
        </span>
        <Button
          aria-expanded={shown}
          aria-controls={regionId}
          onClick={() => setShown((before) => !before)}
        >
          {shown
            ? t("expr.names.hide_usages", { name: written })
            : t("expr.names.show_usages", { name: written })}
        </Button>
        <Link
          to={panel.to}
          params={{ projectId }}
          search={panel.search}
          className={`${textLink} inline-flex min-h-11 items-center`}
          aria-label={
            item.kind === "variable"
              ? t("expr.names.open_variable", { name: written })
              : t("expr.names.open_component", { name: written })
          }
        >
          {item.kind === "variable"
            ? t("expr.names.open_variable_short")
            : t("expr.names.open_component_short")}
        </Link>
      </div>
      <UsageLists
        projectId={projectId}
        id={regionId}
        shown={shown}
        lists={lists}
        className="ps-4"
      />
    </li>
  );
}

/** The names that a compiled source resolves, each with what it resolved to (FR-VAR-02 AC1), and its usages and its panel within two presses (FR-DEP-01 AC3). */
export function Names({
  projectId,
  analysis,
  scope,
  words,
}: {
  projectId: string;
  analysis: Analysis;
  scope: Scope;
  words: Words;
}) {
  const headingId = useId();
  const items = useMemo(() => resolvedOf(analysis), [analysis]);
  // One namer of the board's names for every item, made once of the scope.
  const name = useMemo(() => valueNamer(scope.view, scope.boardId), [scope]);
  if (items.length === 0) {
    return null;
  }
  return (
    <div className="space-y-2 text-sm">
      <p id={headingId} className="font-medium">
        {words.t("expr.names.heading")}
      </p>
      <ul aria-labelledby={headingId} className="space-y-2">
        {items.map((item) => (
          <NameItem
            key={item.key}
            projectId={projectId}
            item={item}
            scope={scope}
            name={name}
            words={words}
          />
        ))}
      </ul>
    </div>
  );
}

/**
 * The offer to create the variable that a name that names nothing would
 * be, inline, of the type that its place suggests and the scope that it is
 * written in, global unless it says board., each of which may be changed:
 * a variable.create of its own, as the page's batches are, once Create is
 * pressed, after which the name names it, and the field has the focus
 * again, as the offer goes, unless the user has given it to a control
 * while the request was answered; a refusal of it is said under the offer
 * of the name that it was of alone, which has the focus on its Create
 * again. Its type is what the name's place suggests, of the literals and
 * the board's names there (unknownOf). A change of its type or its scope
 * is no change of the expression's owner, whose form it does not tell of,
 * so that the form does not hold the owner as it was for it (useHeld).
 */
export function UnknownName({
  projectId,
  analysis,
  scope,
  onCreated,
}: {
  projectId: string;
  analysis: Analysis;
  scope: Scope;
  onCreated: () => void;
}) {
  const { t } = useTranslation();
  const unknown = useMemo(() => unknownOf(analysis, scope), [analysis, scope]);
  const commands = useCommands(projectId);
  // What the user chose of the name of key, which another name starts
  // anew from what its place suggests.
  const [chosen, setChosen] = useState<{
    key: string;
    type?: VariableType;
    scope?: "global" | "board";
  }>();
  // The key of the name that Create was last pressed for.
  const [tried, setTried] = useState<string>();
  const headingId = useId();
  const button = useRef<HTMLButtonElement>(null);
  // The key of the name whose creation was refused, whose offer, which went
  // while the page showed the variable created, and the focus with it, to
  // the page's content (focusContentIfLost), comes again: its Create has the
  // focus then, unless the user has given it to a control meanwhile, and
  // the alert above it says why (WCAG 2.4.3).
  const [refocus, setRefocus] = useState<string>();
  useEffect(() => {
    if (refocus === undefined || refocus !== unknown?.key) {
      return;
    }
    setRefocus(undefined);
    if (focusIsNowhere(document)) {
      button.current?.focus();
    }
  }, [refocus, unknown?.key]);
  if (unknown === undefined) {
    return null;
  }
  const mine = chosen?.key === unknown.key ? chosen : undefined;
  const type = mine?.type ?? unknown.type;
  const where = unknown.scope ?? mine?.scope ?? "global";
  const refusal =
    tried === unknown.key ? refusalOf(commands.error, t) : undefined;
  const create = () => {
    const command: Command = {
      type: "variable.create",
      payload: {
        id: newId("variable"),
        key: unknown.key,
        type,
        scope: where,
        ...(where === "board" ? { board_id: scope.boardId } : {}),
      },
    };
    const { key } = unknown;
    setTried(key);
    setRefocus(undefined);
    commands.mutate([command], {
      onSuccess: () => {
        // What was chosen of the name created alone, not of another that
        // the offer shows by now, as once more was typed meanwhile.
        setChosen((c) => (c?.key === key ? undefined : c));
        // The field has the focus again, of the Create that had it, unless
        // the user has given it to a control meanwhile (WCAG 2.4.3).
        if (
          focusIsNowhere(document) ||
          document.activeElement === button.current
        ) {
          onCreated();
        }
      },
      onError: () => setRefocus(key),
    });
  };
  return (
    <section
      aria-labelledby={headingId}
      className="space-y-2 rounded-md border border-border p-3"
      data-create-variable={unknown.key}
    >
      <p id={headingId} className="font-medium">
        {t("expr.create.heading", { key: unknown.key })}
      </p>
      <div className="grid grid-cols-2 gap-2">
        <SelectField
          label={t("expr.create.scope")}
          value={where}
          disabled={unknown.scope !== undefined}
          onChange={(e) => {
            e.stopPropagation();
            setChosen({
              key: unknown.key,
              type: mine?.type,
              scope: e.currentTarget.value as "global" | "board",
            });
          }}
        >
          <option value="global">{t("expr.create.global")}</option>
          <option value="board">{t("expr.create.board")}</option>
        </SelectField>
        <SelectField
          label={t("expr.create.type")}
          value={type}
          onChange={(e) => {
            e.stopPropagation();
            setChosen({
              key: unknown.key,
              scope: mine?.scope,
              type: e.currentTarget.value as VariableType,
            });
          }}
        >
          {(["bool", "int", "float", "string"] as const).map((option) => (
            <option key={option} value={option}>
              {typeText(t, option)}
            </option>
          ))}
        </SelectField>
      </div>
      {refusal !== undefined && (
        <p role="alert" className="text-sm text-danger">
          {refusal.message}
        </p>
      )}
      <Button ref={button} disabled={commands.isPending} onClick={create}>
        {where === "global"
          ? t("expr.create.create_global", {
              key: unknown.key,
              type: typeText(t, type),
            })
          : t("expr.create.create_board", {
              key: unknown.key,
              type: typeText(t, type),
            })}
      </Button>
    </section>
  );
}
