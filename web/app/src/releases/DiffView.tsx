// The web diff of two releases, or of a release and the draft (PRD
// FR-SNAP-04, §7.4, plan note N88), as the server's entity diff has it: the
// counts of every change of each kind, which hold whatever it left out to
// size it, the changes of the project's own files, and each board's, what
// each changed field held on either side, a rich text's word by word, and,
// collapsed, the changes of position alone (§7.4). A word's change is told
// by its mark as well as its colour.

import type {
  BoardChanges,
  ChangeCounts,
  Diff,
  EntityChange,
  FieldChange,
} from "@storycreep/shared/api";
import { formatNumber } from "@storycreep/shared/i18n";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

/** The kinds of entity, in the order that the summary lists them. */
const kinds = [
  "board",
  "node",
  "branch_output",
  "connection",
  "variable",
  "component_type",
  "attribute",
  "component",
  "project",
] as const;

/** Returns the name of a kind of entity, in the plural, as the summary's rows name them. */
function kindName(t: TFunction, kind: (typeof kinds)[number]): string {
  switch (kind) {
    case "board":
      return t("releases.diff.kinds.board");
    case "node":
      return t("releases.diff.kinds.node");
    case "branch_output":
      return t("releases.diff.kinds.branch_output");
    case "connection":
      return t("releases.diff.kinds.connection");
    case "variable":
      return t("releases.diff.kinds.variable");
    case "component_type":
      return t("releases.diff.kinds.component_type");
    case "attribute":
      return t("releases.diff.kinds.attribute");
    case "component":
      return t("releases.diff.kinds.component");
    case "project":
      return t("releases.diff.kinds.project");
  }
}

/** Returns what happened to an entity, in words. */
function changeText(t: TFunction, change: string): string {
  switch (change) {
    case "added":
      return t("releases.diff.added");
    case "removed":
      return t("releases.diff.removed");
    case "moved":
      return t("releases.diff.moved");
    default:
      return t("releases.diff.modified");
  }
}

/** Returns what names a change's entity, as what its kind shows of it, or its ID, which tells apart those that have no name. */
function changeName(c: EntityChange): string {
  switch (c.kind) {
    case "variable":
      return c.key ?? c.id;
    case "component_type":
    case "attribute":
    case "component":
      return c.name ?? c.key ?? c.id;
    case "node":
      return c.title ?? c.preview ?? c.id;
    case "connection":
      return c.label ?? c.id;
    default:
      return c.id;
  }
}

/** Returns a field's value on one side as text: a string as it is, and any other value as its JSON. */
function valueText(t: TFunction, value: unknown): string {
  if (value === undefined) {
    return t("releases.diff.no_value");
  }
  return typeof value === "string" ? value : JSON.stringify(value);
}

export function DiffView({ diff }: { diff: Diff }) {
  const { t } = useTranslation();
  const { project, summary } = diff;
  const total =
    summary.added +
    summary.removed +
    summary.modified +
    summary.moved +
    summary.cosmetic;
  const lists = [
    ["variables", t("releases.diff.variables"), project.variables],
    [
      "component-types",
      t("releases.diff.component_types"),
      project.componentTypes,
    ],
    ["attributes", t("releases.diff.attributes"), project.attributes],
    ["components", t("releases.diff.components"), project.components],
  ] as const;
  return (
    <div className="space-y-6">
      <Summary diff={diff} />
      {total === 0 && <p>{t("releases.diff.none")}</p>}
      {diff.truncated && (
        <p className="rounded-md border border-border bg-surface px-4 py-3">
          {t("releases.diff.truncated")}
        </p>
      )}
      {project.fields.length > 0 && (
        <section aria-labelledby="diff-project" className="space-y-2">
          <h2 id="diff-project" className="text-xl font-semibold">
            {t("releases.diff.project")}
          </h2>
          <Fields fields={project.fields} />
        </section>
      )}
      {lists.map(
        ([id, heading, changes]) =>
          changes.length > 0 && (
            <section
              key={id}
              aria-labelledby={`diff-${id}`}
              className="space-y-2"
            >
              <h2 id={`diff-${id}`} className="text-xl font-semibold">
                {heading}
              </h2>
              <Changes changes={changes} />
            </section>
          ),
      )}
      {project.omitted > 0 && (
        <p className="text-fg-muted">
          {t("releases.diff.omitted", { count: project.omitted })}
        </p>
      )}
      {diff.boards.map((board) => (
        <BoardSection key={board.board.id} board={board} />
      ))}
    </div>
  );
}

/** The counts of each kind of entity that changed, and of every change. */
function Summary({ diff }: { diff: Diff }) {
  const { t, i18n } = useTranslation();
  const { summary } = diff;
  const number = (n: number) => formatNumber(i18n.language, n);
  const cells = (c: ChangeCounts) =>
    [c.added, c.modified, c.moved, c.removed, c.cosmetic] as const;
  const head = "px-2 py-1 text-end font-medium";
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse">
        <caption className="text-start font-medium">
          {t("releases.diff.summary")}
        </caption>
        <thead>
          <tr className="border-b border-border">
            <th scope="col" className="px-2 py-1 text-start font-medium">
              {t("releases.diff.kind")}
            </th>
            <th scope="col" className={head}>
              {t("releases.diff.added")}
            </th>
            <th scope="col" className={head}>
              {t("releases.diff.modified")}
            </th>
            <th scope="col" className={head}>
              {t("releases.diff.moved")}
            </th>
            <th scope="col" className={head}>
              {t("releases.diff.removed")}
            </th>
            <th scope="col" className={head}>
              {t("releases.diff.cosmetic")}
            </th>
          </tr>
        </thead>
        <tbody>
          {kinds
            .filter((kind) =>
              cells(summary.kinds[kind]).some((count) => count > 0),
            )
            .map((kind) => (
              <tr key={kind} className="border-b border-border">
                <th scope="row" className="px-2 py-1 text-start font-normal">
                  {kindName(t, kind)}
                </th>
                {cells(summary.kinds[kind]).map((count, i) => (
                  <td
                    // biome-ignore lint/suspicious/noArrayIndexKey: the row's columns, which are fixed.
                    key={i}
                    className="px-2 py-1 text-end tabular-nums"
                  >
                    {number(count)}
                  </td>
                ))}
              </tr>
            ))}
          <tr>
            <th scope="row" className="px-2 py-1 text-start font-medium">
              {t("releases.diff.total")}
            </th>
            {cells(summary).map((count, i) => (
              <td
                // biome-ignore lint/suspicious/noArrayIndexKey: as above.
                key={i}
                className="px-2 py-1 text-end font-medium tabular-nums"
              >
                {number(count)}
              </td>
            ))}
          </tr>
        </tbody>
      </table>
    </div>
  );
}

/** A board's changes, those of position alone collapsed (§7.4). */
function BoardSection({ board }: { board: BoardChanges }) {
  const { t } = useTranslation();
  const headingId = `diff-board-${board.board.id}`;
  const shown = board.changes.filter((c) => c.cosmetic !== true);
  const cosmetic = board.changes.filter((c) => c.cosmetic === true);
  return (
    <section aria-labelledby={headingId} className="space-y-2">
      <h2 id={headingId} className="text-xl font-semibold break-words">
        {board.change === undefined
          ? board.board.name
          : t("releases.diff.board_changed", {
              board: board.board.name,
              change: changeText(t, board.change),
            })}
      </h2>
      {board.fields !== undefined && board.fields.length > 0 && (
        <Fields fields={board.fields} />
      )}
      {shown.length > 0 && <Changes changes={shown} />}
      {cosmetic.length > 0 && (
        <details>
          <summary className="flex min-h-11 cursor-pointer items-center">
            {t("releases.diff.cosmetic_changes", { count: cosmetic.length })}
          </summary>
          <Changes changes={cosmetic} />
        </details>
      )}
      {board.omitted > 0 && (
        <p className="text-fg-muted">
          {t("releases.diff.omitted", { count: board.omitted })}
        </p>
      )}
    </section>
  );
}

function Changes({ changes }: { changes: readonly EntityChange[] }) {
  return (
    <ul>
      {changes.map((change) => (
        <ChangeItem key={change.id} change={change} />
      ))}
    </ul>
  );
}

function ChangeItem({ change }: { change: EntityChange }) {
  const { t } = useTranslation();
  return (
    <li className="space-y-1 border-t border-border py-2">
      <p className="break-words">
        <span className="font-medium">{changeText(t, change.change)}</span>
        {": "}
        {changeName(change)}
        {change.type !== undefined && (
          <span className="text-fg-muted">
            {" · "}
            <code>{change.type}</code>
          </span>
        )}
      </p>
      {change.fromBoard !== undefined && change.toBoard !== undefined && (
        <p className="break-words text-fg-muted">
          {t("releases.diff.moved_boards", {
            from: change.fromBoard.name,
            to: change.toBoard.name,
          })}
        </p>
      )}
      {change.fields !== undefined && change.fields.length > 0 && (
        <Fields fields={change.fields} />
      )}
    </li>
  );
}

/** Each changed field, with what it held on either side, a component's value by the ID of its attribute, which is all that the diff names it by. */
function Fields({ fields }: { fields: readonly FieldChange[] }) {
  const { t } = useTranslation();
  return (
    <dl className="space-y-1">
      {fields.map((field) => (
        <div key={`${field.field}:${field.attribute ?? ""}`}>
          <dt className="break-words text-fg-muted">
            <code>{field.field}</code>
            {field.attribute !== undefined && (
              <>
                {" · "}
                <code>{field.attribute}</code>
              </>
            )}
          </dt>
          <dd className="break-words whitespace-pre-wrap">
            {field.words !== undefined && field.words.length > 0 ? (
              <>
                <Words runs={field.words} />
                {field.words.every((run) => run.op === "equal") && (
                  <span className="block text-fg-muted">
                    {t("releases.diff.same_words")}
                  </span>
                )}
              </>
            ) : (
              <>
                <span className="block">
                  {t("releases.diff.before", {
                    value: valueText(t, field.before),
                  })}
                </span>
                <span className="block">
                  {t("releases.diff.after", {
                    value: valueText(t, field.after),
                  })}
                </span>
              </>
            )}
            {field.refsChanged === true && (
              <span className="block text-fg-muted">
                {t("releases.diff.refs_changed")}
              </span>
            )}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/** A rich text's word-level diff: what went is struck through, and what came underlined. */
function Words({ runs }: { runs: NonNullable<FieldChange["words"]> }) {
  return (
    <span>
      {runs.map((run, i) => {
        switch (run.op) {
          case "delete":
            return (
              // biome-ignore lint/suspicious/noArrayIndexKey: the runs of one text, in their order, which change with it.
              <del key={i} className="text-danger line-through">
                {run.text}
              </del>
            );
          case "insert":
            return (
              // biome-ignore lint/suspicious/noArrayIndexKey: as above.
              <ins key={i} className="font-semibold underline">
                {run.text}
              </ins>
            );
          default:
            // biome-ignore lint/suspicious/noArrayIndexKey: as above.
            return <span key={i}>{run.text}</span>;
        }
      })}
    </span>
  );
}
