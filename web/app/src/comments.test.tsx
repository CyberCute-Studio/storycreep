import type {
  Board,
  CommentAuthor,
  CommentBody,
  CommentCount,
  RichTextDoc,
  Variable,
} from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Engine, Frame } from "./canvas/engine";
import { renderApp, testSession } from "./test/app";
import { testNode } from "./test/canvas";
import "./test/editor";
import { fakeApi, refused, startPage, testProject } from "./test/fake";

// A project's threaded comments (PRD FR-COMMENT-01, plan item 74), as the
// router shows them, over a fake of the API's client that keeps them as the
// server does: the threads of a variable's panel, a node's inspector, and
// the project's list of them, with its filters in the address, the canvas's
// badge of a node's open threads, and the search's comments.

// The frames that the canvas drew, whose marks of comments a test reads:
// PixiJS needs WebGL or a 2D canvas, which jsdom has neither of, so the
// page's tests draw with an engine that draws nothing.
const frames: Frame[] = [];
vi.mock("./canvas/engine", () => ({
  createEngine: async (): Promise<Engine> => ({
    kind: "canvas",
    resize: () => {},
    render: (f) => {
      frames.push(f);
      return {
        lod: "detail",
        nodes: f.scene.nodes.size,
        visible: f.scene.nodes.size,
        texts: 0,
        connections: f.scene.connections.size,
      };
    },
    textures: () => undefined,
    onRestore: () => () => {},
    destroy: () => {},
  }),
}));

// jsdom lays nothing out: the canvas is 800 by 600, from the window's
// corner, so that a pointer's point on it is its point in the window.
beforeEach(() => {
  frames.length = 0;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      return this.getAttribute("role") === "application"
        ? new DOMRect(0, 0, 800, 600)
        : new DOMRect();
    },
  );
});

afterEach(() => {
  vi.restoreAllMocks();
});

const at = "2026-11-10T10:00:00Z";
const jo: CommentAuthor = { id: testSession.user_id, display_name: "Jo" };
// IDs as the server writes them, which the list's filters in the address
// take alone.
const mira: CommentAuthor = { id: newId("user"), display_name: "Mira" };

const harbour: Board = {
  id: newId("board"),
  project_id: testProject.id,
  name: "Harbour",
  slug: "harbour",
  description: "",
  color: "none",
  order: "V",
  rev: 3,
  created_at: at,
  updated_at: at,
};

const gold: Variable = {
  id: "var_gold",
  project_id: testProject.id,
  key: "gold",
  type: "int",
  default: 0,
  description: "",
  scope: "global",
  reset_on_enter: false,
  order: "V",
  rev: 1,
  created_at: at,
  updated_at: at,
};

const quay = testNode("nd_1", 0, 0, {
  title: "Quay",
  board_id: harbour.id,
  project_id: testProject.id,
});

/** Returns a rich text of one paragraph of text. */
function paragraph(text: string): RichTextDoc {
  return {
    type: "doc",
    content: [{ type: "paragraph", content: [{ type: "text", text }] }],
  };
}

/** A comment as the fake server keeps it. */
interface Stored {
  id: string;
  parent_id?: string;
  target_type: CommentBody["target_type"];
  target_id: string;
  body: RichTextDoc;
  author: CommentAuthor;
  created_at: string;
  edited_at?: string;
  resolved_at?: string;
  resolved_by?: CommentAuthor;
  deleted: boolean;
  rev: number;
}

/** Returns a first comment of a thread on the target of type and id, by author. */
function thread(
  id: string,
  type: CommentBody["target_type"],
  target: string,
  text: string,
  author = jo,
  more: Partial<Stored> = {},
): Stored {
  return {
    id,
    target_type: type,
    target_id: target,
    body: paragraph(text),
    author,
    created_at: at,
    deleted: false,
    rev: 1,
    ...more,
  };
}

/** Returns a reply to the first comment root, by author. */
function reply(id: string, root: Stored, text: string, author = jo): Stored {
  return {
    ...thread(id, root.target_type, root.target_id, text, author),
    parent_id: root.id,
  };
}

type Sent = { type: string; payload: Record<string, unknown> }[];

/**
 * Answers the project's pages, of the role, and keeps their comments as the
 * server does: a thread is shown while its first comment, or a reply of it,
 * is not deleted, and its target, as the node nd_1, is not in the trash,
 * which node.delete puts it in and node.restore takes it out of; a deleted
 * first comment of a thread that is shown is listed without its body and
 * its author; each command of a comment has its undo; refuse throws its
 * refusal of a batch, if any, before it applies; and listed answers each
 * read of the list with what it makes of the comments listed, as late as
 * it resolves.
 */
function commentProject(
  stored: Stored[],
  options: {
    role?: (typeof testProject)["role"];
    refuse?: (commands: Sent) => Error | undefined;
    search?: (query: Record<string, unknown>) => unknown;
    listed?: (items: CommentBody[]) => Promise<CommentBody[]>;
  } = {},
) {
  const comments = [...stored];
  let seq = testProject.seq;
  let made = 0;
  const trashed = new Set<string>();
  let nodeRev = quay.rev;
  const byId = (id: string) => comments.find((c) => c.id === id);
  const live = (root: Stored) =>
    !(root.target_type === "node" && trashed.has(root.target_id));
  const where = (root: Stored): Partial<CommentBody> => {
    switch (root.target_type) {
      case "node":
        return {
          board_id: harbour.id,
          board_name: harbour.name,
          node_id: root.target_id,
          node_title: quay.title,
        };
      case "board":
        return { board_id: harbour.id, board_name: harbour.name };
      default:
        return {};
    }
  };
  const shown = (c: Stored, root: Stored): CommentBody => ({
    id: c.id,
    project_id: testProject.id,
    thread_id: root.id,
    target_type: root.target_type,
    target_id: root.target_id,
    ...where(root),
    deleted: c.deleted,
    created_at: c.created_at,
    updated_at: c.edited_at ?? c.created_at,
    rev: c.rev,
    ...(c.deleted
      ? {}
      : {
          body: c.body,
          author: c.author,
          ...(c.edited_at === undefined ? {} : { edited_at: c.edited_at }),
        }),
    ...(c.parent_id === undefined
      ? {
          resolved: c.resolved_at !== undefined,
          ...(c.resolved_at === undefined
            ? {}
            : { resolved_at: c.resolved_at, resolved_by: c.resolved_by }),
        }
      : { parent_id: c.parent_id }),
  });
  // Each thread that is shown, newest first, with its replies that are not
  // deleted, oldest first, by when each was posted, and then by its ID, as
  // the server orders them (the times here are all of one form).
  const threads = () =>
    comments
      .filter((c) => c.parent_id === undefined)
      .sort((a, b) => (a.id < b.id ? 1 : -1))
      .map((root) => ({
        root,
        replies: comments
          .filter((c) => c.parent_id === root.id && !c.deleted)
          .sort((a, b) =>
            a.created_at !== b.created_at
              ? a.created_at < b.created_at
                ? -1
                : 1
              : a.id < b.id
                ? -1
                : 1,
          ),
      }))
      .filter(
        ({ root, replies }) =>
          live(root) && (!root.deleted || replies.length > 0),
      );
  const list = (query: Record<string, unknown>): CommentBody[] =>
    threads()
      .filter(
        ({ root, replies }) =>
          (query.target === undefined || root.target_id === query.target) &&
          (query.status === undefined ||
            (query.status === "resolved") ===
              (root.resolved_at !== undefined)) &&
          (query.author === undefined ||
            [root, ...replies].some(
              (c) => !c.deleted && c.author.id === query.author,
            )) &&
          (query.board === undefined || where(root).board_id === query.board),
      )
      .flatMap(({ root, replies }) => [
        shown(root, root),
        ...replies.map((r) => shown(r, root)),
      ]);
  const counts = (): CommentCount[] => {
    const of = new Map<string, CommentCount>();
    for (const { root } of threads()) {
      const count = of.get(root.target_id) ?? {
        target_type: root.target_type,
        target_id: root.target_id,
        open: 0,
        resolved: 0,
      };
      if (root.resolved_at === undefined) {
        count.open++;
      } else {
        count.resolved++;
      }
      of.set(root.target_id, count);
    }
    return [...of.values()];
  };
  const apply = (commands: Sent) => {
    const refusal = options.refuse?.(commands);
    if (refusal !== undefined) {
      throw refusal;
    }
    seq++;
    const revisions: { id: string; rev: number }[] = [];
    const undo: unknown[] = [];
    const now = `2026-11-10T11:00:${String(++made).padStart(2, "0")}Z`;
    for (const { type, payload } of commands) {
      const id = String(payload.id);
      const c = byId(id);
      const changed = (s: Stored, back: unknown) => {
        s.rev++;
        revisions.push({ id: s.id, rev: s.rev });
        undo.unshift(back);
      };
      switch (type) {
        case "comment.create": {
          const parent =
            payload.parent_id === undefined
              ? undefined
              : byId(String(payload.parent_id));
          const target = String(payload.target ?? parent?.target_id);
          comments.push({
            id,
            ...(parent === undefined ? {} : { parent_id: parent.id }),
            target_type:
              parent?.target_type ??
              (target.startsWith("nd_")
                ? "node"
                : target.startsWith("var_")
                  ? "variable"
                  : "board"),
            target_id: target,
            body: payload.body as RichTextDoc,
            author: jo,
            created_at: now,
            deleted: false,
            rev: 1,
          });
          revisions.push({ id, rev: 1 });
          undo.unshift({
            type: "comment.delete",
            payload: { id },
            expected_rev: { [id]: 1 },
          });
          break;
        }
        case "comment.edit":
          if (c !== undefined) {
            const was = c.body;
            c.body = payload.body as RichTextDoc;
            c.edited_at = now;
            changed(c, {
              type: "comment.edit",
              payload: { id, body: was },
              expected_rev: { [id]: c.rev + 1 },
            });
          }
          break;
        case "comment.delete":
        case "comment.restore":
          if (c !== undefined) {
            c.deleted = type === "comment.delete";
            changed(c, {
              type:
                type === "comment.delete"
                  ? "comment.restore"
                  : "comment.delete",
              payload: { id },
              expected_rev: { [id]: c.rev + 1 },
            });
          }
          break;
        case "comment.resolve":
        case "comment.unresolve":
          if (c !== undefined) {
            c.resolved_at = type === "comment.resolve" ? now : undefined;
            c.resolved_by = type === "comment.resolve" ? jo : undefined;
            changed(c, {
              type:
                type === "comment.resolve"
                  ? "comment.unresolve"
                  : "comment.resolve",
              payload: { id },
              expected_rev: { [id]: c.rev + 1 },
            });
          }
          break;
        case "node.delete":
        case "node.restore":
          if (type === "node.delete") {
            trashed.add(id);
          } else {
            trashed.delete(id);
          }
          nodeRev++;
          revisions.push({ id, rev: nodeRev });
          undo.unshift({
            type: type === "node.delete" ? "node.restore" : "node.delete",
            payload: { id },
            expected_rev: {},
          });
          break;
      }
    }
    return { status: "applied", seq, revisions, not_reconnected: [], undo };
  };
  const calls = fakeApi((operation, input) => {
    const call = (input ?? {}) as {
      query?: Record<string, unknown>;
      body?: { commands: Sent };
    };
    switch (operation) {
      case "getProject":
        return { ...testProject, role: options.role ?? "owner", seq };
      case "listFolders":
      case "listBoardConnections":
      case "listComponentTypes":
      case "listComponents":
      case "listNodeReferrers":
        return { items: [], next_cursor: null };
      case "listTags":
        return { items: [] };
      case "listBoards":
        return { items: [harbour], next_cursor: null };
      case "listVariables":
        return { items: [gold], next_cursor: null };
      case "listBoardNodes":
        return {
          items: trashed.has(quay.id) ? [] : [{ ...quay, rev: nodeRev }],
          next_cursor: null,
        };
      case "listComments": {
        const items = list(call.query ?? {});
        return options.listed === undefined
          ? { items, next_cursor: null }
          : options.listed(items).then((listed) => ({
              items: listed,
              next_cursor: null,
            }));
      }
      case "countComments":
        return { items: counts(), next_cursor: null };
      case "listCommentAuthors": {
        const authors = new Map<string, CommentAuthor>();
        for (const { root, replies } of threads()) {
          for (const c of [root, ...replies]) {
            if (!c.deleted) {
              authors.set(c.author.id, c.author);
            }
          }
        }
        return {
          items: [...authors.values()].sort((a, b) => (a.id < b.id ? -1 : 1)),
          next_cursor: null,
        };
      }
      case "searchProject":
        return options.search?.(call.query ?? {});
      case "applyCommands":
        return apply(call.body?.commands ?? []);
    }
    const answer = startPage(operation);
    if (answer === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return answer;
  });
  return {
    calls,
    comments,
    /** The commands of the batches that the page sent, without their IDs. */
    batches: (): Sent[] =>
      calls.mock.calls
        .filter(([operation]) => operation === "applyCommands")
        .map(([, input]) =>
          (
            input as {
              body: { commands: (Sent[number] & { command_id: string })[] };
            }
          ).body.commands.map(({ command_id: _, ...command }) => command),
        ),
    /** The queries of the project's list of comments that the page read, but of a target's. */
    lists: () =>
      calls.mock.calls
        .filter(([operation]) => operation === "listComments")
        .map(([, input]) => (input as { query: Record<string, unknown> }).query)
        .filter((query) => query.target === undefined)
        .map(({ cursor: _, limit: __, ...query }) => query),
  };
}

/** Types text into the rich text of textbox, as the user's keys would. */
function typeInto(textbox: HTMLElement, text: string) {
  act(() => {
    (
      textbox as unknown as {
        editor: { commands: { insertContent: (text: string) => void } };
      }
    ).editor.commands.insertContent(text);
  });
}

/** Writes doc over what the rich text of textbox holds, as the user would. */
function replaceIn(textbox: HTMLElement, doc: RichTextDoc) {
  act(() => {
    (
      textbox as unknown as {
        editor: {
          commands: {
            setContent: (doc: RichTextDoc, o: { emitUpdate: boolean }) => void;
          };
        };
      }
    ).editor.commands.setContent(doc, { emitUpdate: true });
  });
}

/** Opens the panel of the variable gold, and returns its comments. */
async function openGold() {
  renderApp(`/projects/${testProject.id}/variables?variable=${gold.id}`, {
    session: testSession,
  });
  return screen.findByRole("region", { name: "Comments" });
}

/** The page's own Undo of its history, which a comment's action is no step of. */
function historyUndo(): HTMLButtonElement {
  return screen.getByRole("button", { name: "Undo" });
}

describe("a target's threads", () => {
  it("starts a thread, answers it, edits the user's own comment, and resolves and reopens the thread, none of which joins the page's undo", async () => {
    const user = userEvent.setup();
    const server = commentProject([]);
    const comments = await openGold();
    expect(await within(comments).findByText("No comments yet.")).toBeDefined();
    // A comment of white space alone is none, which is not sent.
    await user.click(
      within(comments).getByRole("button", { name: "Post comment" }),
    );
    expect(within(comments).getByText("Write a comment.")).toBeDefined();
    typeInto(
      within(comments).getByRole("textbox", { name: "New comment" }),
      "Is gold ever spent?",
    );
    expect(within(comments).queryByText("Write a comment.")).toBeNull();
    await user.click(
      within(comments).getByRole("button", { name: "Post comment" }),
    );
    expect(await within(comments).findByText("Comment posted.")).toBeDefined();
    expect(
      await within(comments).findByText("Is gold ever spent?"),
    ).toBeDefined();
    const [[create]] = server.batches();
    expect(create).toEqual({
      type: "comment.create",
      payload: {
        id: expect.stringMatching(/^cmt_/),
        target: gold.id,
        body: paragraph("Is gold ever spent?"),
      },
    });
    const id = String(create.payload.id);
    expect(within(comments).getByText("Jo commented")).toBeDefined();
    expect(
      within(comments).getByText("1 open thread, 0 resolved."),
    ).toBeDefined();
    // The form is empty again, for the next comment.
    expect(
      within(comments).getByRole("textbox", { name: "New comment" })
        .textContent,
    ).toBe("");

    await user.click(within(comments).getByRole("button", { name: "Reply" }));
    const answer = within(comments).getByRole("textbox", { name: "Reply" });
    await waitFor(() => expect(document.activeElement).toBe(answer));
    typeInto(answer, "Yes, at the market.");
    await user.click(
      within(comments).getByRole("button", { name: "Post reply" }),
    );
    expect(
      await within(comments).findByText("Yes, at the market."),
    ).toBeDefined();
    expect(server.batches()[1]).toEqual([
      {
        type: "comment.create",
        payload: {
          id: expect.stringMatching(/^cmt_/),
          parent_id: id,
          body: paragraph("Yes, at the market."),
        },
      },
    ]);
    expect(within(comments).getByText("Jo replied")).toBeDefined();
    // The form went, and the thread's Reply has the focus that its Post had.
    await waitFor(() =>
      expect(document.activeElement).toBe(
        within(comments).getByRole("button", { name: "Reply" }),
      ),
    );

    // The first comment's Edit, the first of the two that are the user's.
    await user.click(
      within(comments).getAllByRole("button", { name: "Edit" })[0],
    );
    const edit = within(comments).getByRole("textbox", { name: "Comment" });
    replaceIn(edit, paragraph("Is gold spent?"));
    await user.click(within(comments).getByRole("button", { name: "Save" }));
    expect(await within(comments).findByText("Is gold spent?")).toBeDefined();
    // It expects the revision that the comment had as its field opened.
    expect(server.batches()[2]).toEqual([
      {
        type: "comment.edit",
        payload: { id, body: paragraph("Is gold spent?") },
        expected_rev: { [id]: 1 },
      },
    ]);
    expect(within(comments).getByText("(edited)")).toBeDefined();

    await user.click(within(comments).getByRole("button", { name: "Resolve" }));
    expect(await within(comments).findByText("Thread resolved.")).toBeDefined();
    expect(
      await within(comments).findByText("0 open threads, 1 resolved."),
    ).toBeDefined();
    expect(within(comments).getByText(/^Resolved by Jo/)).toBeDefined();
    await user.click(within(comments).getByRole("button", { name: "Reopen" }));
    expect(await within(comments).findByText("Thread reopened.")).toBeDefined();
    // Each expects the revision that the list showed.
    expect(server.batches().slice(3)).toEqual([
      [{ type: "comment.resolve", payload: { id }, expected_rev: { [id]: 2 } }],
      [
        {
          type: "comment.unresolve",
          payload: { id },
          expected_rev: { [id]: 3 },
        },
      ],
    ]);
    // None of them is a step of the page's undo (plan item 74).
    expect(historyUndo().disabled).toBe(true);
  });

  it("lists a deleted first comment of a thread with replies in its place, without its body and its author, as the server does, with an Undo of its own in its thread that the page's undo is not", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "Lanterns?");
    const theirs = reply("cmt_02", root, "Two of them.", mira);
    const server = commentProject([root, theirs]);
    const comments = await openGold();
    expect(await within(comments).findByText("Lanterns?")).toBeDefined();
    // Mira's reply is hers alone to edit and delete.
    const replies = within(comments).getByRole("list", { name: "1 reply" });
    expect(within(replies).getByText("Mira replied")).toBeDefined();
    expect(within(replies).queryByRole("button", { name: "Edit" })).toBeNull();
    expect(
      within(replies).queryByRole("button", { name: "Delete" }),
    ).toBeNull();

    await user.click(within(comments).getByRole("button", { name: "Delete" }));
    const undo = await within(comments).findByRole("button", {
      name: "Undo the deletion of the comment",
    });
    // The Undo, a line of the thread, takes the focus that Delete had.
    await waitFor(() => expect(document.activeElement).toBe(undo));
    expect(server.batches()).toEqual([
      [
        {
          type: "comment.delete",
          payload: { id: root.id },
          expected_rev: { [root.id]: 1 },
        },
      ],
    ]);
    // The server lists the first comment without its body and its author,
    // and the reply as it was.
    await waitFor(() =>
      expect(within(comments).queryByText("Lanterns?")).toBeNull(),
    );
    const listed = undo.closest("li[data-thread]") as HTMLElement;
    expect(within(listed).getByText("Comment deleted.")).toBeDefined();
    expect(within(listed).getByText("Your comment was deleted.")).toBeDefined();
    expect(within(comments).queryByText("Jo commented")).toBeNull();
    expect(within(listed).getByText("Two of them.")).toBeDefined();
    expect(
      within(comments).getByText("Comment deleted. Undo brings it back."),
    ).toBeDefined();
    expect(historyUndo().disabled).toBe(true);

    // Its undo is what the server answered the deletion with.
    await user.click(undo);
    expect(await within(comments).findByText("Lanterns?")).toBeDefined();
    expect(server.batches()[1]).toEqual([
      {
        type: "comment.restore",
        payload: { id: root.id },
        expected_rev: { [root.id]: 2 },
      },
    ]);
    expect(within(comments).getByText("Comment restored.")).toBeDefined();
    expect(within(comments).queryByText("Comment deleted.")).toBeNull();
    expect(
      within(comments).queryByText("Your comment was deleted."),
    ).toBeNull();
    expect(historyUndo().disabled).toBe(true);
  });

  it("offers the Undo of a thread's only comment above the threads once the server no longer lists the thread, with the focus that Delete had", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "A stray thought.");
    commentProject([root]);
    const comments = await openGold();
    expect(await within(comments).findByText("A stray thought.")).toBeDefined();
    await user.click(within(comments).getByRole("button", { name: "Delete" }));
    await within(comments).findByRole("button", {
      name: "Undo the deletion of the comment",
    });
    await waitFor(() =>
      expect(within(comments).getByText("0 open threads, 0 resolved.")),
    );
    // The threads show none, as the server lists none, and the Undo is a
    // line of their own, which keeps the focus as it leaves the thread.
    expect(within(comments).getByText("No comments yet.")).toBeDefined();
    expect(within(comments).queryByText("A stray thought.")).toBeNull();
    expect(within(comments).queryByText("Comment deleted.")).toBeNull();
    expect(
      within(comments).getByText("Your comment was deleted."),
    ).toBeDefined();
    const undo = within(comments).getByRole("button", {
      name: "Undo the deletion of the comment",
    });
    expect(undo.closest("li[data-thread]")).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(undo));
    await user.click(undo);
    expect(await within(comments).findByText("A stray thought.")).toBeDefined();
    expect(
      within(comments).queryByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeNull();
  });

  it("offers the Undo of a reply above the threads once the server no longer lists its thread, whose first comment was deleted before, until the list's next action", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "Gone.", mira, {
      deleted: true,
    });
    const mine = reply("cmt_02", root, "Still here.");
    const other = thread("cmt_03", "variable", gold.id, "Another thread.");
    commentProject([root, mine, other]);
    const comments = await openGold();
    expect(await within(comments).findByText("Still here.")).toBeDefined();
    const theirs = within(comments)
      .getByText("Still here.")
      .closest("li[data-thread]") as HTMLElement;
    await user.click(within(theirs).getByRole("button", { name: "Delete" }));
    await within(comments).findByRole("button", {
      name: "Undo the deletion of the comment",
    });
    // The thread, which the server lists no more, is gone, and the reply's
    // Undo is a line of the threads' own.
    await waitFor(() =>
      expect(within(comments).getByText("1 open thread, 0 resolved.")),
    );
    expect(within(comments).queryByText("Comment deleted.")).toBeNull();
    expect(within(comments).queryByText("Reply deleted.")).toBeNull();
    expect(within(comments).getByText("Your reply was deleted.")).toBeDefined();
    const undo = within(comments).getByRole("button", {
      name: "Undo the deletion of the comment",
    });
    expect(undo.closest("li[data-thread]")).toBeNull();
    await user.click(undo);
    expect(await within(comments).findByText("Still here.")).toBeDefined();
    expect(within(comments).getByText("Comment deleted.")).toBeDefined();

    // The list's next action ends the Undo shown.
    const again = within(comments)
      .getByText("Still here.")
      .closest("li[data-thread]") as HTMLElement;
    await user.click(within(again).getByRole("button", { name: "Delete" }));
    expect(
      await within(comments).findByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeDefined();
    const another = within(comments)
      .getByText("Another thread.")
      .closest("li[data-thread]") as HTMLElement;
    await user.click(within(another).getByRole("button", { name: "Resolve" }));
    expect(await within(comments).findByText("Thread resolved.")).toBeDefined();
    expect(
      within(comments).queryByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeNull();
  });

  it("keeps a deletion's Undo through a read that was under way as the deletion was sent, answered after it with the comment as it was, until a new thread is posted", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "A stray thought.");
    // What the list was read with before the deletion, which a read that
    // was under way as it was sent is answered with after it.
    let before: CommentBody[] = [];
    let deleting = false;
    let answeredLate = false;
    commentProject([root], {
      refuse: (commands) => {
        deleting ||= commands[0].type === "comment.delete";
        return undefined;
      },
      listed: async (items) => {
        if (!deleting) {
          before = items;
          return items;
        }
        deleting = false;
        await new Promise((resolve) => setTimeout(resolve, 20));
        answeredLate = true;
        return before;
      },
    });
    const comments = await openGold();
    expect(await within(comments).findByText("A stray thought.")).toBeDefined();
    await user.click(within(comments).getByRole("button", { name: "Delete" }));
    expect(
      await within(comments).findByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeDefined();
    await waitFor(() => expect(answeredLate).toBe(true));
    await act(async () => {});
    // The late answer, which still has the comment, which the threads show
    // as the server listed it, leaves the Undo.
    expect(within(comments).getByText("A stray thought.")).toBeDefined();
    expect(
      within(comments).getByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeDefined();

    // A new thread is the threads' next action, which ends the Undo.
    typeInto(
      within(comments).getByRole("textbox", { name: "New comment" }),
      "A better one.",
    );
    await user.click(
      within(comments).getByRole("button", { name: "Post comment" }),
    );
    expect(await within(comments).findByText("A better one.")).toBeDefined();
    await waitFor(() =>
      expect(
        within(comments).queryByRole("button", {
          name: "Undo the deletion of the comment",
        }),
      ).toBeNull(),
    );
    await waitFor(() =>
      expect(within(comments).queryByText("A stray thought.")).toBeNull(),
    );
  });

  it("ends a deletion's Undo that the server refuses for the comment's restoration elsewhere, and shows the comment as it is", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "A stray thought.");
    const server = commentProject([root], {
      refuse: (commands) => {
        if (commands[0].type !== "comment.restore") {
          return undefined;
        }
        // Another session of the user's restored the comment meanwhile.
        root.deleted = false;
        root.rev = 3;
        return refused(409, {
          code: "revision_conflict",
          details: {
            conflicts: [
              {
                command: 0,
                command_id: "00000000-0000-7000-8000-000000000001",
                type: "comment.restore",
                id: root.id,
                expected_rev: 2,
                exists: true,
                current_rev: 3,
              },
            ],
            states: { [root.id]: { id: root.id, rev: 3 } },
          },
        });
      },
    });
    const comments = await openGold();
    expect(await within(comments).findByText("A stray thought.")).toBeDefined();
    await user.click(within(comments).getByRole("button", { name: "Delete" }));
    await user.click(
      await within(comments).findByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    );
    expect(
      await within(comments).findByText(
        "This comment changed elsewhere since you deleted it, so its deletion can no longer be undone here. The comments are shown as they are now.",
      ),
    ).toBeDefined();
    expect(within(comments).getByText("A stray thought.")).toBeDefined();
    expect(
      within(comments).queryByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeNull();
    expect(server.batches()[1]).toEqual([
      {
        type: "comment.restore",
        payload: { id: root.id },
        expected_rev: { [root.id]: 2 },
      },
    ]);
  });

  it("posts a new thread and a reply once, under the IDs that their forms keep, when Post is pressed again after no answer came", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "Lanterns?", mira);
    // The first batch of each comment applies, and its answer is lost.
    const lost = new Set<string>();
    const server = commentProject([root], {
      refuse: (commands) => {
        const id = String(commands[0].payload.id);
        if (commands[0].type !== "comment.create" || lost.has(id)) {
          return undefined;
        }
        lost.add(id);
        return new TypeError("Failed to fetch");
      },
    });
    const comments = await openGold();
    expect(await within(comments).findByText("Lanterns?")).toBeDefined();
    typeInto(
      within(comments).getByRole("textbox", { name: "New comment" }),
      "A new one.",
    );
    const post = within(comments).getByRole("button", {
      name: "Post comment",
    });
    await user.click(post);
    await waitFor(() => expect(server.batches()).toHaveLength(1));
    await user.click(post);
    expect(await within(comments).findByText("A new one.")).toBeDefined();
    const lanterns = within(comments)
      .getByText("Lanterns?")
      .closest("li[data-thread]") as HTMLElement;
    await user.click(within(lanterns).getByRole("button", { name: "Reply" }));
    typeInto(within(lanterns).getByRole("textbox", { name: "Reply" }), "Two.");
    const answer = within(lanterns).getByRole("button", {
      name: "Post reply",
    });
    await user.click(answer);
    await waitFor(() => expect(server.batches()).toHaveLength(3));
    await user.click(answer);
    expect(await within(comments).findByText("Two.")).toBeDefined();
    // Each comment was sent twice, under one ID.
    const created = server
      .batches()
      .map((batch) => batch[0])
      .filter((c) => c.type === "comment.create")
      .map((c) => String(c.payload.id));
    expect(created).toHaveLength(4);
    expect(created[1]).toBe(created[0]);
    expect(created[3]).toBe(created[2]);
    expect(created[2]).not.toBe(created[0]);
  });

  it("shows the threads as the server lists them once a deletion takes a thread out of the list's filter, with the deletion's Undo above them", async () => {
    const user = userEvent.setup();
    // The user, by an ID that the list's filter in the address takes.
    const me: CommentAuthor = { id: newId("user"), display_name: "Jo" };
    const root = thread("cmt_01", "variable", gold.id, "Is it lit?", mira);
    const mine = reply("cmt_02", root, "Not yet.", me);
    commentProject([root, mine]);
    renderApp(`/projects/${testProject.id}/comments?author=${me.id}`, {
      session: { ...testSession, user_id: me.id },
    });
    expect(await screen.findByText("Not yet.")).toBeDefined();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    // The server lists Mira's thread no more, as the user wrote nothing in
    // it that is not deleted: her comment is not shown as deleted.
    expect(
      await screen.findByText("No open thread matches the filters."),
    ).toBeDefined();
    expect(screen.queryByText("Is it lit?")).toBeNull();
    expect(screen.queryByText("Comment deleted.")).toBeNull();
    expect(screen.getByText("Your reply was deleted.")).toBeDefined();
    await user.click(
      screen.getByRole("button", { name: "Undo the deletion of the comment" }),
    );
    expect(await screen.findByText("Not yet.")).toBeDefined();
    expect(screen.getByText("Is it lit?")).toBeDefined();
  });

  it("says that a comment changed elsewhere since its edit began, keeps what was typed, and expects the revision that it is read with then", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "First draft.");
    let elsewhere = false;
    const server = commentProject([root], {
      refuse: (commands) => {
        if (commands[0].type !== "comment.edit" || elsewhere) {
          return undefined;
        }
        // The author's other tab saved an edit of the comment meanwhile.
        elsewhere = true;
        root.body = paragraph("Second draft, elsewhere.");
        root.rev = 2;
        return refused(409, {
          code: "revision_conflict",
          details: {
            conflicts: [
              {
                command: 0,
                command_id: "00000000-0000-7000-8000-000000000001",
                type: "comment.edit",
                id: root.id,
                expected_rev: 1,
                exists: true,
                current_rev: 2,
              },
            ],
            states: { [root.id]: { id: root.id, rev: 2 } },
          },
        });
      },
    });
    const comments = await openGold();
    expect(await within(comments).findByText("First draft.")).toBeDefined();
    await user.click(within(comments).getByRole("button", { name: "Edit" }));
    const edit = within(comments).getByRole("textbox", { name: "Comment" });
    replaceIn(edit, paragraph("Third draft."));
    await user.click(within(comments).getByRole("button", { name: "Save" }));
    expect(
      await within(comments).findByText(
        "This comment changed elsewhere since you began to edit it, so it is shown again as it is now. Your text is kept: save again to keep it.",
      ),
    ).toBeDefined();
    expect(edit.textContent).toBe("Third draft.");
    expect(server.batches()[0]).toEqual([
      {
        type: "comment.edit",
        payload: { id: root.id, body: paragraph("Third draft.") },
        expected_rev: { [root.id]: 1 },
      },
    ]);
    // Saved again, knowing, it expects the revision that the list read.
    await user.click(within(comments).getByRole("button", { name: "Save" }));
    expect(await within(comments).findByText("Third draft.")).toBeDefined();
    expect(server.batches()[1]).toEqual([
      {
        type: "comment.edit",
        payload: { id: root.id, body: paragraph("Third draft.") },
        expected_rev: { [root.id]: 2 },
      },
    ]);
  });

  it("says a refusal of the server's under the comment's field, author_required among them, and keeps what was typed", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "Mine, I thought.");
    commentProject([root], {
      refuse: (commands) => {
        switch (commands[0].type) {
          case "comment.edit":
            return refused(403, {
              code: "author_required",
              details: {
                errors: [
                  {
                    location: "body.commands[0].payload.id",
                    message: "is another user's comment",
                  },
                ],
              },
            });
          case "comment.create":
            return refused(422, {
              code: "validation_failed",
              details: {
                errors: [
                  {
                    location: "body.commands[0].payload.body.content[0]",
                    message: "holds a mention of a component",
                  },
                ],
              },
            });
        }
        return undefined;
      },
    });
    const comments = await openGold();
    expect(await within(comments).findByText("Mine, I thought.")).toBeDefined();

    const field = within(comments).getByRole("textbox", {
      name: "New comment",
    });
    typeInto(field, "Ask @Elara.");
    await user.click(
      within(comments).getByRole("button", { name: "Post comment" }),
    );
    expect(
      await within(comments).findByText("Holds a mention of a component."),
    ).toBeDefined();
    expect(field.getAttribute("aria-invalid")).toBe("true");
    expect(field.textContent).toBe("Ask @Elara.");
    await waitFor(() => expect(document.activeElement).toBe(field));

    await user.click(within(comments).getByRole("button", { name: "Edit" }));
    const edit = within(comments).getByRole("textbox", { name: "Comment" });
    replaceIn(edit, paragraph("Mine, after all."));
    await user.click(within(comments).getByRole("button", { name: "Save" }));
    const said = await within(comments).findByText(
      "Only the author of this comment may change it.",
    );
    // Under the edit's field, which it describes.
    expect(edit.getAttribute("aria-describedby")?.split(" ")).toContain(
      said.id,
    );
    expect(edit.textContent).toBe("Mine, after all.");
  });

  // Verifies: FR-COMMENT-01/AC1
  it("lets a commenter start, answer, and resolve threads, but change nothing of the story", async () => {
    const user = userEvent.setup();
    const root = thread("cmt_01", "variable", gold.id, "Too much gold?", mira);
    const server = commentProject([root], { role: "commenter" });
    const comments = await openGold();
    expect(await within(comments).findByText("Too much gold?")).toBeDefined();
    // The variable is read, not edited: its panel has no form of its own.
    expect(
      screen.queryByRole("button", { name: "Save the variable" }),
    ).toBeNull();
    expect(screen.queryByRole("button", { name: "Rename" })).toBeNull();
    typeInto(
      within(comments).getByRole("textbox", { name: "New comment" }),
      "Halve it.",
    );
    await user.click(
      within(comments).getByRole("button", { name: "Post comment" }),
    );
    expect(await within(comments).findByText("Halve it.")).toBeDefined();
    // Mira's thread, which the commenter resolves, as anyone who comments.
    const theirs = within(comments)
      .getByText("Too much gold?")
      .closest("li") as HTMLElement;
    await user.click(within(theirs).getByRole("button", { name: "Resolve" }));
    expect(await within(comments).findByText("Thread resolved.")).toBeDefined();
    expect(within(theirs).queryByRole("button", { name: "Edit" })).toBeNull();
    expect(server.batches().map((b) => b.map((c) => c.type))).toEqual([
      ["comment.create"],
      ["comment.resolve"],
    ]);
  });

  it("shows a viewer the threads without a form or a button of them", async () => {
    const root = thread("cmt_01", "variable", gold.id, "Too much gold?", mira);
    commentProject([root, reply("cmt_02", root, "Agreed.")], {
      role: "viewer",
    });
    const comments = await openGold();
    expect(await within(comments).findByText("Too much gold?")).toBeDefined();
    expect(within(comments).getByText("Agreed.")).toBeDefined();
    expect(
      within(comments).getByText(
        "You can read the comments here. A commenter, an editor, an admin, or an owner of the project posts and answers them.",
      ),
    ).toBeDefined();
    expect(within(comments).queryByRole("textbox")).toBeNull();
    expect(within(comments).queryAllByRole("button")).toEqual([]);
  });
});

describe("the project's comments", () => {
  it("lists the open threads, each by a link to what it is on, and holds its filters of status, author, and board in the address", async () => {
    const user = userEvent.setup();
    const onQuay = thread("cmt_01", "node", quay.id, "The quay is dark.", mira);
    const onGold = thread("cmt_02", "variable", gold.id, "Gold is cheap.");
    const done = thread("cmt_03", "board", harbour.id, "Rename it.", jo, {
      resolved_at: at,
      resolved_by: jo,
    });
    const server = commentProject([onQuay, onGold, done]);
    const { router } = renderApp(`/projects/${testProject.id}/comments`, {
      session: testSession,
    });
    expect(await screen.findByText("Gold is cheap.")).toBeDefined();
    expect(screen.getByText("The quay is dark.")).toBeDefined();
    expect(screen.queryByText("Rename it.")).toBeNull();
    expect(screen.getByText("2 threads.")).toBeDefined();
    // Each thread's target opens where its comments are.
    expect(
      screen
        .getByRole("link", { name: "On “Quay”, Harbour" })
        .getAttribute("href"),
    ).toBe(
      `/app/projects/${testProject.id}/boards/${harbour.id}?node=nd_1&comments=true`,
    );
    expect(
      screen
        .getByRole("link", { name: "On the variable gold" })
        .getAttribute("href"),
    ).toBe(`/app/projects/${testProject.id}/variables?variable=var_gold`);
    expect(server.lists()).toEqual([{ status: "open" }]);

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Status" }),
      "resolved",
    );
    expect(await screen.findByText("Rename it.")).toBeDefined();
    expect(
      screen
        .getByRole("link", { name: "On the board Harbour" })
        .getAttribute("href"),
    ).toBe(
      `/app/projects/${testProject.id}/boards/${harbour.id}?comments=true`,
    );
    expect(router.state.location.search).toEqual({ status: "resolved" });

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Status" }),
      "all",
    );
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Author" }),
      "Mira",
    );
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        status: "all",
        author: mira.id,
      }),
    );
    expect(await screen.findByText("1 thread.")).toBeDefined();
    expect(screen.getByText("The quay is dark.")).toBeDefined();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Board" }),
      "Harbour",
    );
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        status: "all",
        author: mira.id,
        board: harbour.id,
      }),
    );
    expect(server.lists()).toContainEqual({
      author: mira.id,
      board: harbour.id,
    });
    // Back goes back through them.
    act(() => router.history.back());
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        status: "all",
        author: mira.id,
      }),
    );
  });

  it("ends a deletion's Undo as its filters change, rather than show it under others", async () => {
    const user = userEvent.setup();
    const alone = thread("cmt_01", "variable", gold.id, "Mine alone.");
    const done = thread("cmt_02", "board", harbour.id, "Rename it.", jo, {
      resolved_at: at,
      resolved_by: jo,
    });
    commentProject([alone, done]);
    // The resolved threads, read first, which the list shows at once once
    // its filter is set back to them, before it reads them again.
    renderApp(`/projects/${testProject.id}/comments?status=resolved`, {
      session: testSession,
    });
    expect(await screen.findByText("Rename it.")).toBeDefined();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Status" }),
      "open",
    );
    expect(await screen.findByText("Mine alone.")).toBeDefined();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(
      await screen.findByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeDefined();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "Status" }),
      "resolved",
    );
    expect(await screen.findByText("Rename it.")).toBeDefined();
    expect(screen.queryByText("Comment deleted.")).toBeNull();
    expect(
      screen.queryByRole("button", {
        name: "Undo the deletion of the comment",
      }),
    ).toBeNull();
  });

  it("reads its filters from the address, as a link names them", async () => {
    const onQuay = thread("cmt_01", "node", quay.id, "The quay is dark.", mira);
    const server = commentProject([onQuay]);
    renderApp(
      `/projects/${testProject.id}/comments?status=all&author=${mira.id}&board=nonsense`,
      { session: testSession },
    );
    expect(await screen.findByText("The quay is dark.")).toBeDefined();
    // An ID that is none of its kind is no filter.
    expect(server.lists()).toEqual([{ author: mira.id }]);
    expect(
      (screen.getByRole("combobox", { name: "Status" }) as HTMLSelectElement)
        .value,
    ).toBe("all");
    await waitFor(() =>
      expect(
        (screen.getByRole("combobox", { name: "Author" }) as HTMLSelectElement)
          .value,
      ).toBe(mira.id),
    );
  });

  // Verifies: FR-COMMENT-01/AC2
  it("hides the threads of a node that the page deletes, and shows them again once its undo restores it", async () => {
    const user = userEvent.setup();
    const onQuay = thread("cmt_01", "node", quay.id, "The quay is dark.", mira);
    const server = commentProject([onQuay]);
    renderApp(`/projects/${testProject.id}/boards/${harbour.id}?node=nd_1`, {
      session: testSession,
    });
    const canvas = await screen.findByRole("application", {
      name: "Nodes of Harbour",
    });
    await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
    // The node's inspector shows its thread.
    const inspector = await screen.findByRole("region", { name: "Quay" });
    expect(
      await within(inspector).findByText("The quay is dark."),
    ).toBeDefined();
    const edit = screen.getByRole("group", { name: "Edit the board" });
    await user.click(within(edit).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(canvas.dataset.nodes).toBe("0"));
    expect(server.batches()).toEqual([
      [
        {
          type: "node.delete",
          payload: { id: quay.id },
          expected_rev: { [quay.id]: 1 },
        },
      ],
    ]);
    const nav = screen.getByRole("navigation", {
      name: `Project ${testProject.name}`,
    });
    await user.click(within(nav).getByRole("link", { name: "Comments" }));
    expect(
      await screen.findByText("No open thread matches the filters."),
    ).toBeDefined();
    expect(screen.queryByText("The quay is dark.")).toBeNull();
    // The node's undo, the page's own, brings it back, with its thread.
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(await screen.findByText("The quay is dark.")).toBeDefined();
    expect(server.batches()[1]).toEqual([
      { type: "node.restore", payload: { id: quay.id }, expected_rev: {} },
    ]);
  });
});

describe("the canvas's marks of comments", () => {
  it("counts a node's open threads on its card's badge and in its name in the tree, and opens them in the inspector from the badge", async () => {
    const open = [
      thread("cmt_01", "node", quay.id, "The quay is dark.", mira),
      thread("cmt_02", "node", quay.id, "And cold."),
      thread("cmt_03", "node", quay.id, "Fixed.", jo, {
        resolved_at: at,
        resolved_by: jo,
      }),
    ];
    commentProject(open);
    renderApp(`/projects/${testProject.id}/boards/${harbour.id}`, {
      session: testSession,
    });
    const canvas = await screen.findByRole("application", {
      name: "Nodes of Harbour",
    });
    await waitFor(() => expect(canvas.dataset.ready).toBe("true"));
    await waitFor(() => expect(canvas.dataset.commented).toBe("1"));
    // The card's labels, which its badge counts, are of the open threads.
    const frame = frames.at(-1) as Frame;
    const item = frame.scene.nodes.get(quay.id);
    expect(item === undefined ? undefined : frame.labels(item).comments).toBe(
      2,
    );
    const tree = screen.getByRole("tree", {
      name: "Nodes and connections of Harbour",
    });
    expect(
      within(tree).getByRole("treeitem", {
        name: "Quay, Narration; 2 open comments",
      }),
    ).toBeDefined();
    // A click on the badge, at the card's top right, selects the node and
    // opens its comments in the inspector, whose heading takes the focus.
    const zoom = Number(canvas.dataset.zoom);
    const badge = {
      clientX: (218 - Number(canvas.dataset.x)) * zoom,
      clientY: (20 - Number(canvas.dataset.y)) * zoom,
    };
    const mouse = { pointerId: 1, pointerType: "mouse", button: 0 };
    fireEvent.pointerDown(canvas, { ...mouse, buttons: 1, ...badge });
    fireEvent.pointerUp(canvas, { ...mouse, buttons: 0, ...badge });
    await waitFor(() => expect(canvas.dataset.selected).toBe("1"));
    const inspector = await screen.findByRole("region", { name: "Quay" });
    const heading = within(inspector).getByRole("heading", {
      name: "Comments",
    });
    await waitFor(() => expect(document.activeElement).toBe(heading));
    expect(within(inspector).getByText("And cold.")).toBeDefined();
  });
});

describe("the project's search of comments", () => {
  it("lists a comment found by what its thread is on, which links to where its comments show", async () => {
    commentProject([], {
      search: (query) => ({
        items:
          query.type === "comment"
            ? [
                {
                  type: "comment",
                  id: "cmt_01",
                  title: [],
                  snippet: [
                    { text: "The ", match: false },
                    { text: "lantern", match: true },
                    { text: " is out.", match: false },
                  ],
                  board_id: harbour.id,
                  board_name: harbour.name,
                  node_id: quay.id,
                  node_title: quay.title,
                  target_type: "node",
                  target_id: quay.id,
                },
              ]
            : [],
        next_cursor: null,
      }),
    });
    renderApp(`/projects/${testProject.id}/search?q=lantern&type=comment`, {
      session: testSession,
    });
    const link = await screen.findByRole("link", {
      name: "Comment on “Quay”",
    });
    expect(link.getAttribute("href")).toBe(
      `/app/projects/${testProject.id}/boards/${harbour.id}?node=nd_1&comments=true`,
    );
    const item = link.closest("li") as HTMLElement;
    expect(item.textContent).toContain("Comment · On Harbour");
    expect(within(item).getByText("lantern").tagName).toBe("MARK");
    expect(
      within(screen.getByRole("combobox", { name: "Find" })).getByRole(
        "option",
        { name: "Comment" },
      ),
    ).toBeDefined();
  });
});
