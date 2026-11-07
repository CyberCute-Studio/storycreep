import type { Board } from "@storycreep/shared/api";
import { useSuspenseQuery } from "@tanstack/react-query";
import { createFileRoute, notFound, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import { BoardCanvas, type CanvasOptions } from "../canvas/BoardCanvas";
import type { RendererKind } from "../canvas/engine";
import type { Rect } from "../canvas/viewport";
import {
  ConflictNotice,
  changedOf,
  SaveStatus,
  useEntityForm,
} from "../content/form";
import { boardPart } from "../document/doc";
import { useBoard, useProjectDocument, useTree } from "../document/react";
import { DeleteBoardDialog, payloadField } from "../project/dialogs";
import { type Command, editsProject, projectQuery } from "../project/queries";
import { canonicalId, found } from "../shell/found";
import { NotFound } from "../shell/NotFound";
import { Button } from "../ui/Button";
import {
  SelectField,
  TextAreaField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert } from "../ui/Page";
import { useDocumentTitle } from "../ui/title";

/**
 * The search of the board's page, which the canvas's benchmark and the
 * reference tablet's checklist read (docs/canvas-checklist.md): the
 * renderer to use, whatever the browser's would be, and a scripted run of
 * the canvas, of how many seconds, across which rectangle of the board.
 */
export interface BoardSearch {
  renderer?: RendererKind;
  bench?: "pan" | "panzoom" | "load";
  seconds?: number;
  area?: string;
  warm?: number | "pass";
}

/** The longest run that the benchmark makes, in seconds: an hour. */
const maxBenchSeconds = 3600;

export const Route = createFileRoute("/projects/$projectId/boards/$boardId")({
  // The board's ID as the server writes it, however the address spells it
  // (canonicalId), as the project's.
  params: {
    parse: ({ boardId }) => ({ boardId: canonicalId("board", boardId) }),
  },
  // The page takes the window's width, for its canvas.
  staticData: { wide: true },
  validateSearch: (search: Record<string, unknown>): BoardSearch => {
    const out: BoardSearch = {};
    if (
      search.renderer === "webgl" ||
      search.renderer === "webgpu" ||
      search.renderer === "canvas"
    ) {
      out.renderer = search.renderer;
    }
    if (
      search.bench === "pan" ||
      search.bench === "panzoom" ||
      search.bench === "load"
    ) {
      out.bench = search.bench;
    }
    const seconds = Number(search.seconds);
    if (Number.isFinite(seconds) && seconds > 0) {
      out.seconds = Math.min(maxBenchSeconds, seconds);
    }
    const warm = Number(search.warm);
    if (search.warm === "pass") {
      out.warm = "pass";
    } else if (Number.isFinite(warm) && warm >= 0) {
      out.warm = Math.min(maxBenchSeconds, warm);
    }
    if (typeof search.area === "string" && areaOf(search.area) !== undefined) {
      out.area = search.area;
    }
    return out;
  },
  // The project comes first, as for the tree of its boards, which its
  // document reads, and which holds the board, but for one in the trash. A
  // document that the real-time channel does not keep current, while it is
  // not live, may not have the board yet, as one that another session made
  // since, so it reads the project anew before it says that there is none,
  // and shows what that read failed with, rather than say so after a read
  // that failed.
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
    // The board's nodes and connections, which the canvas shows, start to
    // load as the page does; the canvas says so while they do, and what
    // their read failed with.
    doc.load(boardPart(params.boardId)).catch(() => {});
  },
  // The router keeps no match of the page once it is left, as for the tree
  // of the project's boards: the project's document may be made anew by the
  // time the user is back, without the board yet, which the page would say
  // there is none of until the loader had read the tree.
  gcTime: 0,
  // The page of another board is shown anew, rather than with what the
  // board's form held, or the deletion that it had open, of the one before.
  remountDeps: ({ params }) => params.boardId,
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

// A board's page (PRD FR-PROJ-02, FR-EDIT-01): the canvas of its nodes and
// connections, and under it its name, its slug, its description, and its
// colour, which an editor changes, and its deletion to the project's trash.
// It shows the board as the project's document has it, so a change that
// another session makes shows as it commits (FR-RT-02), and the page of no
// page once the board has gone to the trash.
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

/** Returns the rectangle of a search's area, x,y,width,height of the board, or undefined for one that is none. */
function areaOf(area: string | undefined): Rect | undefined {
  if (area === undefined) {
    return undefined;
  }
  const parts = area.split(",").map(Number);
  if (parts.length !== 4 || !parts.every(Number.isFinite)) {
    return undefined;
  }
  const [x, y, width, height] = parts;
  return width > 0 && height > 0 ? { x, y, width, height } : undefined;
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
  const search = Route.useSearch();
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  useDocumentTitle(
    t("board.page_title", { board: board.name, project: project.name }),
  );
  const options = useMemo<CanvasOptions>(
    () => ({
      renderer: search.renderer,
      bench:
        search.bench === undefined
          ? undefined
          : {
              scenario: search.bench,
              seconds: search.seconds ?? 30,
              area: areaOf(search.area),
              warm: search.warm,
            },
    }),
    [search.renderer, search.bench, search.seconds, search.area, search.warm],
  );
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">{board.name}</h1>
      <BoardCanvas
        // A renderer asked for anew starts the canvas anew.
        key={search.renderer ?? ""}
        projectId={projectId}
        boardId={board.id}
        boardName={board.name}
        settingsId="board-settings"
        options={options}
      />
      <section
        aria-labelledby="board-settings"
        className="mx-auto max-w-3xl space-y-6"
      >
        {/* The canvas's toolbar leads here, and gives the heading the focus. */}
        <h2 id="board-settings" tabIndex={-1} className="text-lg font-semibold">
          {t("board.settings_title")}
        </h2>
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
      </section>
    </div>
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
 * AC3). It shows the board as the page has it until the user is in it, with
 * the focus, or has changed something in it, and then as it was, so that a
 * change made elsewhere, which the page's document shows as it commits, does
 * not take away the focus, a selection, or what the user typed (FR-RT-02
 * AC3), until the focus leaves it unchanged, or it is saved (useHeld). A
 * save refused for a change made elsewhere of another field is sent again by
 * the document (PRD §7.10); one of a field that the form changes too names
 * each such field with both values, and shows the board as it is now, but
 * for what the user changed, which it keeps, as a save does that the
 * document could not send again, for how often the board changed meanwhile,
 * so that saving again keeps it, over the board as it is now, and what the
 * user changes after is over what the form shows, rather than over another
 * session's change that it did not show; using theirs shows the board as it
 * is now (useEntityForm).
 */
function BoardForm({ projectId, board }: { projectId: string; board: Board }) {
  const { t } = useTranslation();
  const form = useEntityForm(projectId, board, (view, id) =>
    view.boards.get(id),
  );
  const { commands, held, shown, against } = form;
  const refusal = refusalOf(commands.error, t, boardField, {
    slug_taken: t("board.error.slug_taken"),
  });
  useFocusRefusal(held.form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const expected_rev = { [against.id]: against.rev };
    // A field is sent where it is not what the board has already: as the
    // form showed it, or as the server had it once it refused the form's
    // save, which the form shows but for what the user changed.
    const typed = changedOf(
      {
        name: String(data.get("name")),
        slug: String(data.get("slug")),
        description: String(data.get("description")),
        color: String(data.get("color")) as Board["color"],
      },
      against,
    );
    const changes: Command[] = [
      ...(typed.name === undefined
        ? []
        : [
            {
              type: "board.rename" as const,
              payload: { id: shown.id, name: typed.name },
              expected_rev,
            },
          ]),
      ...(typed.slug === undefined
        ? []
        : [
            {
              type: "board.set_slug" as const,
              payload: { id: shown.id, slug: typed.slug },
              expected_rev,
            },
          ]),
      ...(typed.description === undefined
        ? []
        : [
            {
              type: "board.describe" as const,
              payload: { id: shown.id, description: typed.description },
              expected_rev,
            },
          ]),
      ...(typed.color === undefined
        ? []
        : [
            {
              type: "board.set_color" as const,
              payload: { id: shown.id, color: typed.color },
              expected_rev,
            },
          ]),
    ];
    form.save(changes, typed);
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
      <Alert
        message={form.overlaps.length > 0 ? undefined : refusal?.message}
      />
      <ConflictNotice
        form={form}
        fieldName={fieldName}
        shownValue={shownValue}
      />
      <SaveStatus form={form} />
      {/* Shown anew once what it shows of the board has changed, as once
          this form saved it, rather than showing what it was. */}
      <form
        key={held.key}
        {...held.props}
        className="space-y-4"
        onSubmit={submit}
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
      <h3 id="board-delete" className="font-semibold">
        {t("board.delete_title")}
      </h3>
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
