import type { Board } from "@storycreep/shared/api";
import { useSuspenseQuery } from "@tanstack/react-query";
import {
  createFileRoute,
  Link,
  notFound,
  useNavigate,
} from "@tanstack/react-router";
import { type FormEvent, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { BoardCanvas, type CanvasOptions } from "../canvas/BoardCanvas";
import type { RendererKind } from "../canvas/engine";
import type { Rect } from "../canvas/viewport";
import { useCommandHandlers } from "../commands/registry";
import {
  ConflictNotice,
  changedOf,
  SaveStatus,
  useEntityForm,
} from "../content/form";
import { boardPart } from "../document/doc";
import { useBoard, useProjectDocument, useTree } from "../document/react";
import type { ProjectDocument } from "../document/store";
import { colorName, paletteColors } from "../project/colors";
import { DeleteBoardDialog, payloadField } from "../project/dialogs";
import { useLastOpened } from "../project/lastOpened";
import {
  type Command,
  editsProject,
  loadPart,
  projectQuery,
  readBoard,
} from "../project/queries";
import { ScriptView } from "../script/ScriptView";
import { canonicalId, found } from "../shell/found";
import { NotFound } from "../shell/NotFound";
import { Button, buttonClass, currentClass } from "../ui/Button";
import {
  SelectField,
  TextAreaField,
  TextField,
  useFocusRefusal,
} from "../ui/Field";
import { Alert } from "../ui/Page";
import { useDocumentTitle } from "../ui/title";

/**
 * The search of the board's page: the view that it shows of the board, its
 * script view in place of its canvas (plan item 68); the node that a link
 * opens the canvas on (plan item 61), as a usage list's, or whose entry the
 * script view shows; and what the canvas's benchmark and the reference
 * tablet's checklist read (docs/canvas-checklist.md): the renderer to use,
 * whatever the browser's would be, and a scripted run of the canvas, of how
 * many seconds, across which rectangle of the board.
 */
export interface BoardSearch {
  view?: "script";
  node?: string;
  renderer?: RendererKind;
  bench?: "pan" | "panzoom" | "load" | "edit";
  seconds?: number;
  area?: string;
  warm?: number | "pass";
}

/** The longest run that the benchmark makes, in seconds: an hour. */
const maxBenchSeconds = 3600;

/**
 * Returns the number that a value of the search spells, as `2`, which the
 * router reads as the number, or `2.5` in a string, or NaN for any other,
 * as an empty value, or `null`, which Number would count as 0, rather than
 * as no value.
 */
function searchNumber(value: unknown): number {
  if (typeof value === "number") {
    return value;
  }
  return typeof value === "string" && value.trim() !== ""
    ? Number(value)
    : Number.NaN;
}

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
    if (search.view === "script") {
      out.view = "script";
    }
    // A node's ID as the server writes it, however the link spells it, as
    // the canvas finds the node by it; one that the board does not have
    // opens the canvas as without it.
    if (typeof search.node === "string" && search.node !== "") {
      out.node = canonicalId("node", search.node);
    }
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
      search.bench === "load" ||
      search.bench === "edit"
    ) {
      out.bench = search.bench;
    }
    const seconds = searchNumber(search.seconds);
    if (Number.isFinite(seconds) && seconds > 0) {
      out.seconds = Math.min(maxBenchSeconds, seconds);
    }
    const warm = searchNumber(search.warm);
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
  // The project, and the tree of its boards, which holds the board, but for
  // one in the trash, as readBoard reads them, which the app's start page
  // reads too before it opens the board: the page of no page where the
  // server has none of either for the user, or the tree no such board, and
  // otherwise what a read failed with, rather than say that there is no
  // board after a read that failed.
  loader: async ({ context, params }) => {
    const doc = await readBoard(
      context,
      params.projectId,
      params.boardId,
      found,
    );
    if (doc === undefined) {
      throw notFound();
    }
    // The board's nodes and connections, which the canvas shows, start to
    // load as the page does, and are read anew where the document had read
    // them already, as the parts that the project's other pages show are
    // (loadPart). The canvas says so while they load, and what this read
    // failed with, which the loader hands to it, rather than start a read
    // of its own as it mounts, which, after this one failed before it, as
    // while the page's chunk was fetched, would ask the server again, and
    // say what the second failed with a round trip later.
    const read = loadPart(doc, boardPart(params.boardId));
    read.catch(() => {});
    return { doc, read };
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

// A board's page (PRD FR-PROJ-02, FR-EDIT-01): the canvas of its nodes and
// connections, or its script view (FR-TEXT-04), which its links switch
// between, and under it its name, its slug, its description, and its
// colour, which an editor changes, and its deletion to the project's trash.
// It shows the board as the project's document has it, so a change that
// another session makes shows as it commits (FR-RT-02), and the page of no
// page once the board has gone to the trash.
function BoardPage() {
  const { projectId, boardId } = Route.useParams();
  const doc = useProjectDocument(projectId);
  const board = useBoard(doc, boardId);
  // The loader's read of the board's content, of the document that the
  // page shows, as it is unless the page's documents made the project's
  // anew since, as once the session was of another account.
  const loaded = Route.useLoaderData();
  const read = loaded.doc === doc ? loaded.read : undefined;
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
      doc={doc}
      board={shown}
      read={read}
      onDeleting={setDeleting}
    />
  );
}

/** Returns the rectangle of a search's area, x,y,width,height of the board, or undefined for one that is none. */
function areaOf(area: string | undefined): Rect | undefined {
  if (area === undefined) {
    return undefined;
  }
  const parts = area.split(",").map(searchNumber);
  if (parts.length !== 4 || !parts.every(Number.isFinite)) {
    return undefined;
  }
  const [x, y, width, height] = parts;
  return width > 0 && height > 0 ? { x, y, width, height } : undefined;
}

function BoardContent({
  projectId,
  doc,
  board,
  read,
  onDeleting,
}: {
  projectId: string;
  /** The project's document, which the page shows the board of. */
  doc: ProjectDocument;
  board: Board;
  /** The read of the board's nodes and connections that the loader started. */
  read: Promise<void> | undefined;
  /** Tells whether the board's deletion is open. */
  onDeleting: (deleting: boolean) => void;
}) {
  const { t } = useTranslation();
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const search = Route.useSearch();
  const editable =
    editsProject(project.role) && project.archived_at === undefined;
  // The device opens this board next, and its canvas where the user left
  // it, with its selection (PRD FR-PROJ-04), but for the benchmark's runs,
  // which start where they choose, and leave nothing for the next, and once
  // the page has deleted it.
  const userId = useSuspenseQuery(sessionQuery).data?.user_id;
  const last = useLastOpened(
    search.bench === undefined ? userId : undefined,
    projectId,
    board.id,
  );
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
  const script = search.view === "script";
  // The loader's read, which the view that mounts takes as its first, as
  // the canvas and the script view do, but once the document holds the
  // board's content of a read since, as Try again's in one view after the
  // loader's read failed, the view shown in place of it reads what the
  // document holds, rather than say again what the loader's read failed
  // with; while it holds that of the loader's own, a view's read is that.
  const firstRead = doc.holds(boardPart(board.id)) ? undefined : read;
  const navigate = useNavigate();
  // The views are commands of the palette too (FR-EDIT-10), each of which
  // can run while the page shows the other.
  const show = (view: "canvas" | "script") => ({
    enabled: script !== (view === "script"),
    run: () =>
      void navigate({
        to: "/projects/$projectId/boards/$boardId",
        params: { projectId, boardId: board.id },
        search: view === "script" ? { view: "script" } : {},
      }),
  });
  useCommandHandlers("page", {
    "board.show_canvas": show("canvas"),
    "board.show_script": show("script"),
  });
  return (
    <div className="space-y-6">
      {/* The views' switch is beside the board's name, rather than under
      it, as the canvas takes the window's height below where it starts. */}
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h1 className="text-2xl font-semibold">{board.name}</h1>
        <nav aria-label={t("board.views")} className="flex flex-wrap gap-2">
          {/* The router marks the link of the view shown current, as it
          matches the page's search: the canvas's names no view, which its
          link says it does not (explicitUndefined), as else every search
          would match it, the script view's too. */}
          <Link
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: board.id }}
            search={{ view: undefined }}
            activeOptions={{ explicitUndefined: true }}
            className={`${buttonClass()} ${currentClass}`}
          >
            {t("board.view_canvas")}
          </Link>
          <Link
            to="/projects/$projectId/boards/$boardId"
            params={{ projectId, boardId: board.id }}
            search={{ view: "script" }}
            className={`${buttonClass()} ${currentClass}`}
          >
            {t("board.view_script")}
          </Link>
        </nav>
      </div>
      {script ? (
        <ScriptView
          projectId={projectId}
          board={board}
          read={firstRead}
          editable={editable}
          focus={search.node}
        />
      ) : (
        <BoardCanvas
          projectId={projectId}
          boardId={board.id}
          boardName={board.name}
          settingsId="board-settings"
          read={firstRead}
          options={options}
          initial={last.initial}
          onView={last.onView}
          workspaceId={project.workspace_id}
          editable={editable}
          focus={search.node}
        />
      )}
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
            onDeleted={last.forget}
          />
        )}
      </section>
    </div>
  );
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
          {paletteColors.map((color) => (
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
  onDeleted,
}: {
  projectId: string;
  board: Board;
  onDeleting: (deleting: boolean) => void;
  /** Tells that the board is deleted, before the page goes. */
  onDeleted: () => void;
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
          // the document's tree, and shows the page of no page. Neither the
          // start page nor the server opens it next (onDeleted).
          onDeleted={() => {
            deleted.current = true;
            onDeleted();
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
