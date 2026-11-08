// Where a project's content uses a variable or a component (PRD FR-DEP-01,
// FR-COMP-04): each site with its board and its node, in lists that the
// server answers a page at a time, one list for each use, as the reads and
// the writes of a variable, which a panel opens on demand, and which a
// refusal of a deletion lists too, the first hundred of them (FR-VAR-04).
// A site of a node, or of a connection or an output of one, is a link that
// opens the node on its board's canvas, selected and shown whole (plan item
// 61), and a component's description one that opens the component's panel.

import type { PageUsage, Usage } from "@storycreep/shared/api";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { type ReactNode, useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useShallow } from "zustand/react/shallow";
import { api, pagedQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useDocumentState, useProjectDocument } from "../document/react";
import { Button, MoreButton } from "../ui/Button";
import { Alert, textLink } from "../ui/Page";

/** How a site uses a variable or a component, as its usage lists say. */
export type Use = "read" | "write" | "speaker" | "mention";

/**
 * Returns the key of a usage among a list's: its site, with what it is of,
 * and the attribute that it reads or writes, of a site that reads or writes
 * several of a component's, which the list holds once for each.
 */
export function usageKey(usage: Usage): string {
  return [
    usage.site,
    usage.connection_id ??
      usage.branch_output_id ??
      usage.node_id ??
      usage.component_id,
    usage.attribute_id,
  ].join(":");
}

/** A usage as a refusal of a deletion lists it, with how the site uses what it deletes. */
export type FoundUsage = Usage & { use: Use };

/** The reads or the writes of a variable, a page at a time. */
export function variableUsagesQuery(
  projectId: string,
  variableId: string,
  access: "read" | "write",
) {
  return pagedQuery(
    ["projects", projectId, "variables", variableId, "usages", access],
    async (query, signal): Promise<PageUsage> =>
      (
        await api.call(
          "listVariableUsages",
          {
            path: { project_id: projectId, variable_id: variableId },
            query: { ...query, access },
          },
          { signal },
        )
      ).body,
    { meta: { showsRefusals: true } },
  );
}

/** The usages of a component of one use, a page at a time. */
export function componentUsagesQuery(
  projectId: string,
  componentId: string,
  use: Use,
) {
  return pagedQuery(
    ["projects", projectId, "components", componentId, "usages", use],
    async (query, signal): Promise<PageUsage> =>
      (
        await api.call(
          "listComponentUsages",
          {
            path: { project_id: projectId, component_id: componentId },
            query: { ...query, use },
          },
          { signal },
        )
      ).body,
    { meta: { showsRefusals: true } },
  );
}

/**
 * Returns the names of the project's components by their IDs, as the
 * project's document has them, by which a list of usages names the
 * components' descriptions that it lists. On a page that has not read the
 * components, as the variables' page, the document reads them once the
 * list holds such a description, so that it is named by its component
 * rather than as a component's; a read that failed, which the document
 * records, leaves it named so. The list follows the names of the
 * components that it lists alone, rather than every change of the
 * project's components.
 */
export function useComponentNames(
  projectId: string,
  usages: readonly Usage[],
): (id: string) => string | undefined {
  const doc = useProjectDocument(projectId);
  const ids = [...new Set(usages.flatMap((usage) => usage.component_id ?? []))];
  const names = useDocumentState(
    doc,
    useShallow((state) => ids.map((id) => state.view.components.get(id)?.name)),
  );
  const describes = ids.length > 0;
  useEffect(() => {
    if (describes && !doc.holds("components")) {
      void doc.load("components").catch(() => undefined);
    }
  }, [doc, describes]);
  return (id) => names[ids.indexOf(id)];
}

/**
 * A site that uses a variable or a component: what it is, of its node, a
 * link that opens the node on its board's canvas, and the board that it is
 * on, or, of a component's description, the component, by its name, whose
 * panel it links to.
 */
export function UsageItem({
  projectId,
  usage,
  use,
  componentName,
}: {
  projectId: string;
  usage: Usage;
  /** How it uses what it is listed for, as a refusal of a deletion says. */
  use?: Use;
  /** The names of the project's components, which a component's description is named by (useComponentNames). */
  componentName: (id: string) => string | undefined;
}) {
  const { t } = useTranslation();
  const component =
    usage.component_id === undefined
      ? undefined
      : componentName(usage.component_id);
  const node =
    usage.node_title === undefined || usage.node_title === ""
      ? t("boards.untitled_node")
      : usage.node_title;
  const site =
    usage.site === "component_description"
      ? t("usage.component_description", {
          component: component ?? t("usage.a_component"),
        })
      : t("usage.site", { site: usage.site, node });
  return (
    <li className="break-words">
      {use !== undefined && (
        <span className="text-fg-muted">{t("usage.use", { use })} · </span>
      )}
      {usage.board_id !== undefined && usage.node_id !== undefined ? (
        <>
          <Link
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: usage.board_id }}
            search={{ node: usage.node_id }}
            className={textLink}
          >
            {site}
          </Link>
          {" · "}
          {usage.board_name ?? t("boards.unknown_board")}
        </>
      ) : (
        site
      )}
      {usage.component_id !== undefined && (
        <>
          {" · "}
          <Link
            to="/projects/$projectId/components"
            params={{ projectId }}
            search={{ component: usage.component_id }}
            className={textLink}
          >
            {t("usage.open_component")}
          </Link>
        </>
      )}
    </li>
  );
}

/** One list of a thing's usages, read a page at a time once it is shown. */
function UsageList({
  projectId,
  title,
  empty,
  query,
}: {
  projectId: string;
  title: string;
  empty: string;
  query: ReturnType<typeof variableUsagesQuery>;
}) {
  const { t } = useTranslation();
  const list = useInfiniteQuery(query);
  const titleId = useId();
  const items = list.data?.pages.flatMap((page) => page.items) ?? [];
  const componentName = useComponentNames(projectId, items);
  return (
    <section aria-labelledby={titleId} className="space-y-1">
      <h4 id={titleId} className="font-medium">
        {title}
      </h4>
      {list.isPending ? (
        <p className="text-fg-muted">{t("usage.loading")}</p>
      ) : list.isError ? (
        <Alert message={refusalOf(list.error, t)?.message} />
      ) : items.length === 0 ? (
        <p className="text-fg-muted">{empty}</p>
      ) : (
        <ul className="list-disc space-y-1 ps-5" aria-labelledby={titleId}>
          {items.map((usage) => (
            <UsageItem
              key={usageKey(usage)}
              projectId={projectId}
              usage={usage}
              componentName={componentName}
            />
          ))}
        </ul>
      )}
      <MoreButton list={list}>{t("usage.more")}</MoreButton>
    </section>
  );
}

/** A list of a thing's usages that Usages shows. */
export interface UsageListOf {
  key: string;
  title: string;
  empty: string;
  query: ReturnType<typeof variableUsagesQuery>;
}

/**
 * The usages of a variable or a component, which the button under heading
 * shows and hides: each of lists, read as they are shown, and again each
 * time that they are shown anew.
 */
export function Usages({
  projectId,
  heading,
  lists,
}: {
  projectId: string;
  heading: ReactNode;
  lists: UsageListOf[];
}) {
  const { t } = useTranslation();
  const [shown, setShown] = useState(false);
  const regionId = useId();
  return (
    <section className="space-y-2">
      <h3 className="text-lg font-semibold">{heading}</h3>
      <Button
        aria-expanded={shown}
        aria-controls={regionId}
        onClick={() => setShown((before) => !before)}
      >
        {shown ? t("usage.hide") : t("usage.show")}
      </Button>
      <div id={regionId} className="space-y-3">
        {shown &&
          lists.map((list) => (
            <UsageList
              key={list.key}
              projectId={projectId}
              title={list.title}
              empty={list.empty}
              query={list.query}
            />
          ))}
      </div>
    </section>
  );
}
