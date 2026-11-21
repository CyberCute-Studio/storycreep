// A release's content, read-only (PRD FR-SNAP-02 AC2, plan note N88): its
// boards, each as a document of its nodes, in the order of the script view,
// with their texts, choices, conditions, and scripts, its variables, and its
// components, as the release's files hold them. It reads those files alone,
// and has no control that changes anything and no part that sends a
// command, so that nothing here can emit one.

import type {
  Connection,
  Node,
  Segment,
  Variable,
} from "@storycreep/shared/published";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { type ReactNode, useState } from "react";
import { useTranslation } from "react-i18next";
import { nodeTitleText } from "../content/textNames";
import { marked } from "../script/RichText";
import { Button, currentClass } from "../ui/Button";
import { textLink } from "../ui/Page";
import type { ReleaseBoard, ReleaseContent } from "./content";

/** How many items a list shows at first, and how many more each Show more shows. */
const shownAtOnce = 100;

/** Returns what names a node: its title, or else its text's preview, which tells apart those without a title. */
function nodeName(t: TFunction, node: Node | undefined): string {
  const preview = node?.type === "element" ? node.text?.preview : undefined;
  return nodeTitleText(t, node?.title || preview || "");
}

/** A text's segments as the page shows them: its paragraphs, an empty one as a blank line, and list items, as plain blocks, each of its inline segments with its marks. */
function SegmentsText({ segments }: { segments: readonly Segment[] }) {
  // Each paragraph or list item begins a block of the inline segments after
  // it, and what comes before the first is a block of its own, which is an
  // empty first paragraph where a paragraph segment is the first, since the
  // format gives the first paragraph none, and nothing before a first item.
  const blocks: {
    key: number;
    lead?: string;
    body: ReactNode[];
  }[] = [{ key: 0, body: [] }];
  segments.forEach((segment, key) => {
    const block = blocks[blocks.length - 1];
    const inline = (shown: string, className?: string) => {
      if (segment.kind === "break" || !("marks" in segment)) {
        return;
      }
      const marks = segment.marks.map((mark) =>
        mark === "link"
          ? { type: "link" as const, attrs: { href: segment.href ?? "" } }
          : { type: mark },
      );
      block.body.push(
        // biome-ignore lint/suspicious/noArrayIndexKey: a text's segments, in their order, which change with it.
        <span key={key} className={className}>
          {marked(shown, marks as Parameters<typeof marked>[1])}
        </span>,
      );
    };
    switch (segment.kind) {
      case "paragraph":
        blocks.push({ key: key + 1, body: [] });
        break;
      case "item":
        blocks.push({
          key: key + 1,
          lead: segment.list === "bullet" ? "•" : `${segment.number}.`,
          body: [],
        });
        break;
      case "break":
        // biome-ignore lint/suspicious/noArrayIndexKey: as above.
        block.body.push(<br key={key} />);
        break;
      case "text":
        inline(segment.value);
        break;
      case "mention":
        inline(`@${segment.value}`, "font-medium");
        break;
      case "value":
        inline(`{${segment.name}}`, "font-mono");
        break;
    }
  });
  return (
    <>
      {blocks
        .filter(
          (block, i) =>
            i > 0 || block.body.length > 0 || segments[0]?.kind === "paragraph",
        )
        .map((block) => (
          <p key={block.key} className="break-words whitespace-pre-wrap">
            {block.lead !== undefined && `${block.lead} `}
            {block.body.length === 0 && block.lead === undefined ? (
              <br />
            ) : (
              block.body
            )}
          </p>
        ))}
    </>
  );
}

/** Returns the items of list that Show more has shown so far. */
function useShown<T>(list: readonly T[]) {
  const [count, setCount] = useState(shownAtOnce);
  return {
    items: list.slice(0, count),
    rest: Math.max(0, list.length - count),
    more: () => setCount(count + shownAtOnce),
  };
}

/** The button that shows more of a list, while it has more. */
function ShowMore({ rest, more }: { rest: number; more: () => void }) {
  const { t } = useTranslation();
  return rest > 0 ? (
    <Button onClick={more}>{t("releases.view.more", { count: rest })}</Button>
  ) : null;
}

export function ReleaseView({
  content,
  boardId,
}: {
  content: ReleaseContent;
  /** The board that the address names, or the first if it names none of the release's. */
  boardId: string | undefined;
}) {
  const { t } = useTranslation();
  const board =
    content.boards.find((b) => b.id === boardId) ?? content.boards[0];
  return (
    <div className="space-y-6">
      <section aria-labelledby="release-boards" className="space-y-3">
        <h2 id="release-boards" className="text-xl font-semibold">
          {t("releases.view.boards")}
        </h2>
        {board === undefined ? (
          <p>{t("releases.view.no_boards")}</p>
        ) : (
          <>
            <nav aria-labelledby="release-boards">
              <ul className="flex flex-wrap gap-2">
                {content.boards.map((b) => (
                  <li key={b.id}>
                    <Link
                      from="/projects/$projectId/releases/$snapshotId"
                      to="."
                      search={{ board: b.id }}
                      replace
                      aria-current={b.id === board.id ? "page" : undefined}
                      className={`inline-flex min-h-11 items-center rounded-md border border-border px-3 hover:bg-surface-hover ${
                        b.id === board.id ? currentClass : ""
                      }`}
                    >
                      {b.name}
                    </Link>
                  </li>
                ))}
              </ul>
            </nav>
            <BoardContent key={board.id} board={board} content={content} />
          </>
        )}
      </section>
      <VariablesList content={content} />
      <ComponentsList content={content} />
    </div>
  );
}

/** A board's nodes, those that its entry reaches first. */
function BoardContent({
  board,
  content,
}: {
  board: ReleaseBoard;
  content: ReleaseContent;
}) {
  const { t } = useTranslation();
  const all = [...board.reached, ...board.unreached];
  const { items, rest, more } = useShown(all);
  const firstUnreached = board.reached.length;
  return (
    <div className="space-y-3">
      <h3 className="text-lg font-semibold break-words">{board.name}</h3>
      {all.length === 0 && <p>{t("releases.view.empty_board")}</p>}
      <ol className="space-y-3">
        {items.map((node, i) => (
          <li key={node.id}>
            {/* A board with no entry node reaches none, and has no such part. */}
            {i === firstUnreached && i > 0 && (
              <p className="pb-2 font-medium">{t("releases.view.unreached")}</p>
            )}
            <NodeEntry node={node} board={board} content={content} />
          </li>
        ))}
      </ol>
      <ShowMore rest={rest} more={more} />
    </div>
  );
}

/** A node, as its type shows it, with its scripts and the choices that leave it. A jumper names its target, with a link that opens the target's board where that is another. */
function NodeEntry({
  node,
  board,
  content,
}: {
  node: Node;
  board: ReleaseBoard;
  content: ReleaseContent;
}) {
  const { t } = useTranslation();
  const speaker =
    node.type === "element" && node.speaker !== undefined
      ? content.components.find((c) => c.id === node.speaker)?.name
      : undefined;
  const target =
    node.type === "jumper" && node.target !== undefined
      ? content.nodes.get(node.target.nodeId)
      : undefined;
  const jumpsTo =
    target === undefined
      ? undefined
      : t("releases.view.jumps_to", {
          node: nodeName(t, target.node),
          board: target.board.name,
        });
  const leaving = board.leaving.get(node.id) ?? [];
  return (
    <article
      aria-label={nodeName(t, node)}
      className="space-y-2 rounded-md border border-border p-3"
    >
      <h4 className="font-medium break-words">
        {nodeName(t, node)}
        {node.id === board.entryNodeId && (
          <span className="text-fg-muted"> · {t("releases.view.entry")}</span>
        )}
        {node.type === "element" && node.isEnding && (
          <span className="text-fg-muted"> · {t("releases.view.ending")}</span>
        )}
      </h4>
      {speaker !== undefined && (
        <p className="text-fg-muted">
          {t("releases.view.speaker", { name: speaker })}
        </p>
      )}
      {node.type === "element" && node.text !== undefined && (
        <SegmentsText segments={node.text.segments} />
      )}
      {node.type === "jumper" && (
        <p>
          {target === undefined ? (
            t("releases.view.no_target")
          ) : target.board.id === board.id ? (
            jumpsTo
          ) : (
            <Link
              from="/projects/$projectId/releases/$snapshotId"
              to="."
              search={{ board: target.board.id }}
              replace
              className={textLink}
            >
              {jumpsTo}
            </Link>
          )}
        </p>
      )}
      {(node.type === "element" || node.type === "branch") && (
        <>
          <Code
            label={t("releases.view.on_enter")}
            code={node.onEnter?.source}
          />
          <Code label={t("releases.view.on_exit")} code={node.onExit?.source} />
        </>
      )}
      {node.type === "branch" && (
        <ul className="space-y-2">
          {node.outputs.map((output) => (
            <li key={output.id} className="space-y-1">
              <p className="font-medium">
                {output.kind === "else" ? (
                  t("releases.view.else")
                ) : (
                  <>
                    {output.kind === "if"
                      ? t("releases.view.if")
                      : t("releases.view.elseif")}{" "}
                    <code className="font-mono">{output.condition.source}</code>
                  </>
                )}
              </p>
              <Choices
                choices={leaving.filter((c) => c.fromPort === output.id)}
                nodes={content.nodes}
              />
            </li>
          ))}
        </ul>
      )}
      {node.type === "element" && (
        <Choices choices={leaving} nodes={content.nodes} />
      )}
    </article>
  );
}

/** A script or a condition's source, if it has one. */
function Code({ label, code }: { label: string; code: string | undefined }) {
  return code === undefined ? null : (
    <p className="break-words">
      <span className="text-fg-muted">{label} </span>
      <code className="font-mono whitespace-pre-wrap">{code}</code>
    </p>
  );
}

/** The connections that leave a port, each with its label, condition, script, and where it leads. */
function Choices({
  choices,
  nodes,
}: {
  choices: readonly Connection[];
  nodes: ReleaseContent["nodes"];
}) {
  const { t } = useTranslation();
  if (choices.length === 0) {
    return null;
  }
  return (
    <ul className="space-y-1 border-s border-border ps-3">
      {choices.map((choice) => (
        <li key={choice.id} className="space-y-1">
          {choice.label !== undefined && (
            <SegmentsText segments={choice.label.segments} />
          )}
          <p className="text-fg-muted break-words">
            {t("releases.view.leads_to", {
              node: nodeName(t, nodes.get(choice.to)?.node),
            })}
          </p>
          <Code
            label={t("releases.view.condition")}
            code={choice.condition?.source}
          />
          <Code
            label={t("releases.view.on_traverse")}
            code={choice.onTraverse?.source}
          />
        </li>
      ))}
    </ul>
  );
}

/** Returns a variable's default as text. */
function defaultText(variable: Variable): string {
  return typeof variable.default === "string"
    ? variable.default
    : JSON.stringify(variable.default);
}

function VariablesList({ content }: { content: ReleaseContent }) {
  const { t } = useTranslation();
  const { items, rest, more } = useShown(content.variables);
  const boardName = (id: string) =>
    content.boards.find((b) => b.id === id)?.name ?? id;
  return (
    <section aria-labelledby="release-variables" className="space-y-2">
      <h2 id="release-variables" className="text-xl font-semibold">
        {t("releases.view.variables")}
      </h2>
      {content.variables.length === 0 && (
        <p>{t("releases.view.no_variables")}</p>
      )}
      <ul>
        {items.map((variable) => (
          <li
            key={variable.id}
            className="space-y-1 border-t border-border py-2"
          >
            <p className="break-words">
              <code className="font-mono">{variable.key}</code>
              <span className="text-fg-muted">
                {" · "}
                {variable.type}
                {" · "}
                {variable.scope === "board"
                  ? t("releases.view.scope_board", {
                      board: boardName(variable.boardId),
                    })
                  : t("releases.view.scope_global")}
              </span>
            </p>
            <p className="break-words text-fg-muted">
              {t("releases.view.default", { value: defaultText(variable) })}
            </p>
            {variable.description !== undefined && (
              <p className="break-words">{variable.description}</p>
            )}
          </li>
        ))}
      </ul>
      <ShowMore rest={rest} more={more} />
    </section>
  );
}

function ComponentsList({ content }: { content: ReleaseContent }) {
  const { t } = useTranslation();
  const { items, rest, more } = useShown(content.components);
  const typeName = (id: string) =>
    content.componentTypes.find((c) => c.id === id)?.name ?? id;
  return (
    <section aria-labelledby="release-components" className="space-y-2">
      <h2 id="release-components" className="text-xl font-semibold">
        {t("releases.view.components")}
      </h2>
      {content.components.length === 0 && (
        <p>{t("releases.view.no_components")}</p>
      )}
      <ul>
        {items.map((component) => (
          <li
            key={component.id}
            className="border-t border-border py-2 break-words"
          >
            {component.name}
            <span className="text-fg-muted">
              {" · "}
              <code className="font-mono">{component.key}</code>
              {" · "}
              {typeName(component.componentTypeId)}
            </span>
          </li>
        ))}
      </ul>
      <ShowMore rest={rest} more={more} />
    </section>
  );
}
