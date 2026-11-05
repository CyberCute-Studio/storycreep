import type { Board } from "@storycreep/shared/api";
import { useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { DeleteBoardDialog, payloadField } from "../project/dialogs";
import {
  boardQuery,
  type Command,
  conflictOf,
  dropBoard,
  editsProject,
  projectQuery,
  treeQuery,
  useCommands,
} from "../project/queries";
import { canonicalId, found } from "../shell/found";
import { Button } from "../ui/Button";
import {
  SelectField,
  TextAreaField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert, Page, Status } from "../ui/Page";

export const Route = createFileRoute("/projects/$projectId/boards/$boardId")({
  // The board's ID as the server writes it, however the address spells it
  // (canonicalId), as the project's.
  params: {
    parse: ({ boardId }) => ({ boardId: canonicalId("board", boardId) }),
  },
  // The project comes first, as for the tree of its boards, and then the
  // board, which is not there once it is in the trash.
  loader: async ({ context: { queryClient }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    await found(
      Promise.all([
        queryClient.ensureQueryData(boardQuery(project.id, params.boardId)),
        queryClient.ensureQueryData(treeQuery(project.id)),
      ]),
    );
  },
  component: BoardPage,
});

/** The colours that a board may have (PRD FR-PROJ-02): none, or a name of the palette. */
const colors = [
  "none",
  "red",
  "orange",
  "yellow",
  "green",
  "teal",
  "blue",
  "purple",
  "pink",
  "gray",
] as const satisfies readonly Board["color"][];

// A board's page (PRD FR-PROJ-02): its name, its slug, its description, and
// its colour, which an editor changes, and its deletion to the project's
// trash. The canvas of its nodes joins it later (plan item 59).
function BoardPage() {
  const { t } = useTranslation();
  const { projectId, boardId } = Route.useParams();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const board = useSuspenseQuery(boardQuery(projectId, boardId)).data;
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  return (
    <Page
      title={board.name}
      documentTitle={t("board.page_title", {
        board: board.name,
        project: project.name,
      })}
      width="wide"
    >
      <p className="text-fg-muted">{t("board.no_canvas")}</p>
      {editable ? (
        <BoardForm projectId={projectId} board={board} />
      ) : (
        <dl className="grid gap-x-6 gap-y-1 sm:grid-cols-[max-content_1fr]">
          <dt className="text-fg-muted">{t("board.slug")}</dt>
          <dd className="break-words">{board.slug}</dd>
          <dt className="text-fg-muted">{t("board.description")}</dt>
          <dd className="break-words whitespace-pre-line">
            {board.description === ""
              ? t("board.no_description")
              : board.description}
          </dd>
          <dt className="text-fg-muted">{t("board.color")}</dt>
          <dd>{colorName(t, board.color)}</dd>
        </dl>
      )}
      {editable && <DeleteBoard projectId={projectId} board={board} />}
    </Page>
  );
}

/** Returns the name of a board's colour, in the page's locale. */
function colorName(
  t: ReturnType<typeof useTranslation>["t"],
  color: Board["color"],
): string {
  switch (color) {
    case "none":
      return t("board.color_none");
    case "red":
      return t("board.color_red");
    case "orange":
      return t("board.color_orange");
    case "yellow":
      return t("board.color_yellow");
    case "green":
      return t("board.color_green");
    case "teal":
      return t("board.color_teal");
    case "blue":
      return t("board.color_blue");
    case "purple":
      return t("board.color_purple");
    case "pink":
      return t("board.color_pink");
    case "gray":
      return t("board.color_gray");
  }
}

const boardField = payloadField("name", "slug", "description", "color");

/**
 * The form of a board's settings, which sends what of them changed in one
 * batch, each command expecting the revision that the form shows, so that a
 * board that changed elsewhere since is not changed over (PRD FR-API-03
 * AC3). It shows the board as the page has it until the user changes
 * something in it, and then as it was, so that a change made elsewhere,
 * which the page reads as its tab is shown again, does not take away what
 * the user typed, until the form is saved, or refused for that change, when
 * it shows the board as it is now.
 */
function BoardForm({ projectId, board }: { projectId: string; board: Board }) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const commands = useCommands(projectId);
  const [unchanged, setUnchanged] = useState(false);
  const [held, setHeld] = useState<Board>();
  const shown = held ?? board;
  const refusal = refusalOf(commands.error, t, boardField, {
    slug_taken: t("board.error.slug_taken"),
  });
  useFocusRefusal(form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const expected_rev = { [shown.id]: shown.rev };
    const name = String(data.get("name"));
    const slug = String(data.get("slug"));
    const description = String(data.get("description"));
    const color = String(data.get("color")) as Board["color"];
    const changes: Command[] = [
      ...(name === shown.name
        ? []
        : [
            {
              type: "board.rename" as const,
              payload: { id: shown.id, name },
              expected_rev,
            },
          ]),
      ...(slug === shown.slug
        ? []
        : [
            {
              type: "board.set_slug" as const,
              payload: { id: shown.id, slug },
              expected_rev,
            },
          ]),
      ...(description === shown.description
        ? []
        : [
            {
              type: "board.describe" as const,
              payload: { id: shown.id, description },
              expected_rev,
            },
          ]),
      ...(color === shown.color
        ? []
        : [
            {
              type: "board.set_color" as const,
              payload: { id: shown.id, color },
              expected_rev,
            },
          ]),
    ];
    setUnchanged(changes.length === 0);
    if (changes.length === 0) {
      commands.reset();
      setHeld(undefined);
      return;
    }
    commands.mutate(changes, {
      onSuccess: () => setHeld(undefined),
      onError: (error) => {
        if (conflictOf(error)) {
          setHeld(undefined);
        }
      },
    });
  };
  return (
    <div className="max-w-md space-y-4">
      <Alert message={refusal?.message} />
      <Status
        message={
          unchanged
            ? t("board.unchanged")
            : commands.isSuccess
              ? t("board.saved")
              : undefined
        }
      />
      {/* Shown anew once the board that it shows has changed, as once
          this form saved it, rather than showing what it was. */}
      <form
        key={shown.rev}
        ref={form}
        className="space-y-4"
        onSubmit={submit}
        onChange={() => setHeld((before) => before ?? board)}
        noValidate
      >
        <TextField
          label={t("board.name")}
          name="name"
          autoComplete="off"
          required
          maxLength={100}
          defaultValue={shown.name}
          error={refusal?.fields.name}
        />
        <TextField
          label={t("board.slug")}
          hint={t("board.slug_hint")}
          name="slug"
          autoComplete="off"
          spellCheck={false}
          required
          maxLength={64}
          defaultValue={shown.slug}
          error={refusal?.fields.slug}
        />
        <TextAreaField
          label={t("board.description")}
          name="description"
          defaultValue={shown.description}
          error={refusal?.fields.description}
        />
        <SelectField
          label={t("board.color")}
          name="color"
          defaultValue={shown.color}
          error={refusal?.fields.color}
        >
          {colors.map((color) => (
            <option key={color} value={color}>
              {colorName(t, color)}
            </option>
          ))}
        </SelectField>
        <Button type="submit" variant="primary" disabled={commands.isPending}>
          {t("board.save")}
        </Button>
      </form>
    </div>
  );
}

/** The button that deletes the board to the project's trash, after the project's page. */
function DeleteBoard({
  projectId,
  board,
}: {
  projectId: string;
  board: Board;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const client = useQueryClient();
  const boards = useSuspenseQuery(treeQuery(projectId)).data.boards;
  const [opener, setOpener] = useState<HTMLElement | null>();
  return (
    <section className="space-y-2" aria-labelledby="board-delete">
      <h2 id="board-delete" className="text-lg font-semibold">
        {t("board.delete_title")}
      </h2>
      <p>{t("board.delete_intro")}</p>
      <Button
        variant="danger"
        aria-haspopup="dialog"
        onClick={(event) => setOpener(event.currentTarget)}
      >
        {t("board.delete")}
      </Button>
      {opener !== undefined && (
        <DeleteBoardDialog
          projectId={projectId}
          board={board}
          boards={boards}
          opener={opener}
          onClose={() => setOpener(undefined)}
          // The page goes before what it read of the board, which is in the
          // trash now, is dropped, so that a page of it that the user goes
          // back to asks the server, which answers as for one that does
          // not exist, rather than shows it.
          onDeleted={() =>
            void navigate({
              to: "/projects/$projectId",
              params: { projectId },
              replace: true,
            }).then(() => dropBoard(client, projectId, board.id))
          }
        />
      )}
    </section>
  );
}
