import type { TrashItemBody } from "@storycreep/shared/api";
import { formatDate, formatRelativeTime } from "@storycreep/shared/i18n";
import {
  useSuspenseInfiniteQuery,
  useSuspenseQuery,
} from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import {
  type Command,
  editsProject,
  projectQuery,
  trashQuery,
  useCommands,
} from "../project/queries";
import { found } from "../shell/found";
import { Button, MoreButton } from "../ui/Button";
import { Alert, Page, Status } from "../ui/Page";

export const Route = createFileRoute("/projects/$projectId/trash")({
  // The project comes first, as for the tree of its boards.
  loader: async ({ context: { queryClient }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    await found(queryClient.ensureInfiniteQueryData(trashQuery(project.id)));
  },
  component: Trash,
});

// What is in a project's trash (PRD FR-PROJ-06): its boards, nodes,
// variables, and components, the most recently deleted first, each until
// its 30 days have passed, which an editor restores where it was, while the
// project is not archived.
function Trash() {
  const { t } = useTranslation();
  const { projectId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const list = useSuspenseInfiniteQuery(trashQuery(projectId));
  const items = list.data.pages.flatMap((page) => page.items);
  const restores =
    editsProject(project.role) && project.archived_at === undefined;
  // What the page says of what it restored, which goes from the list with
  // its item.
  const [restored, setRestored] = useState<string>();
  return (
    <Page
      title={t("trash.title")}
      documentTitle={t("trash.page_title", { project: project.name })}
      width="wide"
    >
      <p>{t("trash.intro")}</p>
      <Status message={restored} />
      {items.length === 0 ? (
        <p className="text-fg-muted">{t("trash.empty")}</p>
      ) : (
        <ul
          className="space-y-3"
          aria-label={t("trash.list", { project: project.name })}
        >
          {items.map((item) => (
            <TrashItem
              key={item.id}
              projectId={projectId}
              item={item}
              restores={restores}
              onRestored={setRestored}
            />
          ))}
        </ul>
      )}
      <MoreButton list={list}>{t("trash.more")}</MoreButton>
    </Page>
  );
}

/** Returns the name of the kind of what is in the trash. */
function kindName(t: TFunction, kind: TrashItemBody["kind"]): string {
  switch (kind) {
    case "board":
      return t("trash.kind_board");
    case "node":
      return t("trash.kind_node");
    case "variable":
      return t("trash.kind_variable");
    case "component":
      return t("trash.kind_component");
  }
}

/** Returns the command that restores item where it was, as board.restore does a board. */
function restoreCommand(item: TrashItemBody): Command {
  const payload = { id: item.id };
  switch (item.kind) {
    case "board":
      return { type: "board.restore", payload };
    case "node":
      return { type: "node.restore", payload };
    case "variable":
      return { type: "variable.restore", payload };
    case "component":
      return { type: "component.restore", payload };
  }
}

function TrashItem({
  projectId,
  item,
  restores,
  onRestored,
}: {
  projectId: string;
  item: TrashItemBody;
  restores: boolean;
  onRestored: (message: string) => void;
}) {
  const { t, i18n } = useTranslation();
  const nameId = useId();
  const locale = i18n.language;
  const commands = useCommands(projectId);
  const name = item.name === "" ? t("trash.untitled") : item.name;
  const restore = () =>
    commands.mutate([restoreCommand(item)], {
      onSuccess: (result) => {
        // A node's connections whose node at the other end is still in the
        // trash come back once that node does (PRD FR-PROJ-06 AC1).
        const waiting = result.not_reconnected?.length ?? 0;
        onRestored(
          waiting === 0
            ? t("trash.restored", { name })
            : t("trash.restored_waiting", { name, count: waiting }),
        );
      },
    });
  return (
    <li className="space-y-2 rounded-md border border-border p-4">
      <p id={nameId} className="font-medium break-words">
        {name}
      </p>
      <dl className="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
        <dt className="text-fg-muted">{t("trash.kind")}</dt>
        <dd>{kindName(t, item.kind)}</dd>
        <dt className="text-fg-muted">{t("trash.deleted")}</dt>
        <dd>{formatRelativeTime(locale, new Date(item.deleted_at))}</dd>
        <dt className="text-fg-muted">{t("trash.purged")}</dt>
        <dd>{formatDate(locale, new Date(item.purge_after))}</dd>
      </dl>
      <Alert message={refusalOf(commands.error, t)?.message} />
      {restores && (
        <Button
          aria-describedby={nameId}
          disabled={commands.isPending}
          onClick={restore}
        >
          {t("trash.restore")}
        </Button>
      )}
    </li>
  );
}
