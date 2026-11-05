import type { Board } from "@storycreep/shared/api";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, notFound, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { useBoard, useProjectDocument, useTree } from "../document/react";
import { ConflictError } from "../document/store";
import { DeleteBoardDialog, payloadField } from "../project/dialogs";
import {
  type Command,
  conflictOf,
  editsProject,
  projectQuery,
  useCommands,
} from "../project/queries";
import { canonicalId, found } from "../shell/found";
import { NotFound } from "../shell/NotFound";
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
  // The project comes first, as for the tree of its boards, which its
  // document reads, and which holds the board, but for one in the trash. A
  // document that the real-time channel does not keep current, while it is
  // not live, may not have the board yet, as one that another session made
  // since, so it reads the project anew before it says that there is none.
  loader: async ({ context: { queryClient, documents }, params }) => {
    const project = await found(
      queryClient.ensureQueryData(projectQuery(params.projectId)),
    );
    const doc = documents.get(project.id);
    await found(doc.load("tree"));
    const has = () => doc.store.getState().view.boards.has(params.boardId);
    if (!has()) {
      await doc.refresh();
    }
    if (!has()) {
      throw notFound();
    }
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
// trash. The canvas of its nodes joins it later (plan item 59). It shows the
// board as the project's document has it, so a change that another session
// makes shows as it commits (FR-RT-02), and the page of no page once the
// board has gone to the trash.
function BoardPage() {
  const { projectId, boardId } = Route.useParams();
  const doc = useProjectDocument(projectId);
  const board = useBoard(doc, boardId);
  // While the board's deletion is open, the board is shown as it was, as
  // the document takes it away at once, once its deletion is sent, so that
  // the dialog stays, and says what became of the deletion, after which the
  // page goes to the project's, or the deletion is cancelled.
  const [deleting, setDeleting] = useState(false);
  const last = useRef<Board>(undefined);
  if (board !== undefined) {
    last.current = board;
  }
  const shown = board ?? (deleting ? last.current : undefined);
  return shown === undefined ? (
    <NotFound />
  ) : (
    <BoardContent
      projectId={projectId}
      board={shown}
      onDeleting={setDeleting}
    />
  );
}

function BoardContent({
  projectId,
  board,
  onDeleting,
}: {
  projectId: string;
  board: Board;
  /** Tells whether the board's deletion is open. */
  onDeleting: (deleting: boolean) => void;
}) {
  const { t } = useTranslation();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
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
      {editable && (
        <DeleteBoard
          projectId={projectId}
          board={board}
          onDeleting={onDeleting}
        />
      )}
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
 * which the page's document shows as it commits, does not take away what
 * the user typed (FR-RT-02 AC3), until the form is saved. A save refused for
 * a change made elsewhere of another field is sent again by the document
 * (PRD §7.10); one of a field that the form changes too names each such
 * field with both values (ConflictError), and keeps what the user typed, so
 * that saving again keeps it, over the board as it is now, without what the
 * user did not change, and using theirs shows the board as it is now.
 */
function BoardForm({ projectId, board }: { projectId: string; board: Board }) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const commands = useCommands(projectId);
  const [unchanged, setUnchanged] = useState(false);
  const [held, setHeld] = useState<Board>();
  // The board as the server had it when it refused the form's save for a
  // change of what the form changes too, which saving again changes over.
  const [theirs, setTheirs] = useState<Board>();
  const shown = held ?? board;
  const against = theirs ?? shown;
  const overlaps =
    commands.error instanceof ConflictError
      ? commands.error.overlaps.flatMap((o) => o.fields)
      : [];
  const refusal = refusalOf(commands.error, t, boardField, {
    slug_taken: t("board.error.slug_taken"),
  });
  useFocusRefusal(form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const expected_rev = { [against.id]: against.rev };
    const name = String(data.get("name"));
    const slug = String(data.get("slug"));
    const description = String(data.get("description"));
    const color = String(data.get("color")) as Board["color"];
    // A field is sent where the user changed it from what the form showed,
    // or where it was one of a conflict's, which saving again keeps, and it
    // is not what the board has already: a field that the user did not
    // change, which the form still shows as it was before a conflict, is
    // not sent over the board as it is now, which another session changed.
    const conflicting = new Set(overlaps.map((o) => o.field));
    const unchangedField = (field: keyof Board, value: unknown) =>
      value === against[field] ||
      (value === shown[field] && !conflicting.has(field));
    const changes: Command[] = [
      ...(unchangedField("name", name)
        ? []
        : [
            {
              type: "board.rename" as const,
              payload: { id: shown.id, name },
              expected_rev,
            },
          ]),
      ...(unchangedField("slug", slug)
        ? []
        : [
            {
              type: "board.set_slug" as const,
              payload: { id: shown.id, slug },
              expected_rev,
            },
          ]),
      ...(unchangedField("description", description)
        ? []
        : [
            {
              type: "board.describe" as const,
              payload: { id: shown.id, description },
              expected_rev,
            },
          ]),
      ...(unchangedField("color", color)
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
      setTheirs(undefined);
      return;
    }
    commands.mutate(changes, {
      onSuccess: () => {
        setHeld(undefined);
        setTheirs(undefined);
      },
      onError: (error) => {
        const state =
          error instanceof ConflictError
            ? error.overlaps.find((o) => o.id === against.id && !o.gone)?.state
            : undefined;
        if (state !== undefined) {
          setTheirs(state as unknown as Board);
        } else if (conflictOf(error)) {
          setHeld(undefined);
          setTheirs(undefined);
        }
      },
    });
  };
  const useTheirs = () => {
    commands.reset();
    setHeld(undefined);
    setTheirs(undefined);
  };
  const fieldName = (field: string) =>
    field === "name"
      ? t("board.name")
      : field === "slug"
        ? t("board.slug")
        : field === "description"
          ? t("board.description")
          : t("board.color");
  const shownValue = (field: string, value: unknown) =>
    field === "color"
      ? colorName(t, value as Board["color"])
      : String(value ?? "");
  return (
    <div className="max-w-md space-y-4">
      <Alert message={overlaps.length > 0 ? undefined : refusal?.message} />
      {overlaps.length > 0 && (
        <div className="space-y-2 rounded-md border border-danger px-4 py-3">
          <p>{t("conflict.intro")}</p>
          <ul className="list-disc ps-5">
            {overlaps.map((o) => (
              <li key={o.field} className="break-words">
                {t("conflict.field", {
                  field: fieldName(o.field),
                  theirs: shownValue(o.field, o.theirs),
                  mine: shownValue(o.field, o.mine),
                })}
              </li>
            ))}
          </ul>
          <Button onClick={useTheirs}>{t("conflict.use_theirs")}</Button>
        </div>
      )}
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
  onDeleting,
}: {
  projectId: string;
  board: Board;
  onDeleting: (deleting: boolean) => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const boards = useTree(useProjectDocument(projectId)).boards;
  const [opener, setOpener] = useState<HTMLElement | null>();
  // Once the board is deleted, the page goes, and shows the board until it
  // has, rather than the page of no page.
  const deleted = useRef(false);
  return (
    <section className="space-y-2" aria-labelledby="board-delete">
      <h2 id="board-delete" className="text-lg font-semibold">
        {t("board.delete_title")}
      </h2>
      <p>{t("board.delete_intro")}</p>
      <Button
        variant="danger"
        aria-haspopup="dialog"
        onClick={(event) => {
          onDeleting(true);
          setOpener(event.currentTarget);
        }}
      >
        {t("board.delete")}
      </Button>
      {opener !== undefined && (
        <DeleteBoardDialog
          projectId={projectId}
          board={board}
          boards={boards}
          opener={opener}
          onClose={() => {
            setOpener(undefined);
            if (!deleted.current) {
              onDeleting(false);
            }
          }}
          // The page goes to the project's; a page of the board, which is
          // in the trash now, that the user goes back to finds it gone from
          // the document's tree, and shows the page of no page.
          onDeleted={() => {
            deleted.current = true;
            void navigate({
              to: "/projects/$projectId",
              params: { projectId },
              replace: true,
            });
          }}
        />
      )}
    </section>
  );
}
