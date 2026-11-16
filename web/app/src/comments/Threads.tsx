// The threads of a project's comments (PRD FR-COMMENT-01, plan item 74): the
// section of what they are on, a node's or a connection's inspector, a
// board's page, or a variable's panel, with its form of a new thread at its
// top and its threads newest first, and the list of threads that the
// project's comment list shows a page at a time, each naming what it is on
// (targetOf). Each thread shows its first comment and its replies, oldest
// first, each with its author, when it was posted, whether it was edited,
// and its body, read-only, with Reply and Resolve, or Reopen, on the thread,
// which anyone whose role allows "Create/resolve comments" uses (§5.6), and
// Edit and Delete on the user's own comments, which only their author may
// (the server refuses anyone else with author_required). A viewer, and every
// role on an archived project, reads them with no form. A comment's actions
// are no steps of the page's undo (sendComments): a deleted comment shows in
// its place with an Undo of its own, which sends the undo that the server
// answered its deletion with, for as long as the section is shown. A first
// comment deleted while its thread has replies shows as deleted, without its
// body and its author, as the server lists it.

import type {
  CommentBody,
  RichTextDoc,
  UndoCommandBody,
} from "@storycreep/shared/api";
import { formatDateTime, formatRelativeTime } from "@storycreep/shared/i18n";
import { newId } from "@storycreep/shared/ids";
import { segmentsOf } from "@storycreep/shared/richtext";
import { useMutation, useQuery, useSuspenseQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import {
  type FormEvent,
  type ReactNode,
  type RefObject,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { useProjectDocument } from "../document/react";
import type { ProjectDocument } from "../document/store";
import { projectQuery } from "../project/queries";
import type { RichTextValue } from "../richtext/convert";
import { RichTextEditor } from "../richtext/RichTextEditor";
import { RichText } from "../script/RichText";
import { focusIsNowhere } from "../shell/focus";
import { Button, LastingButton } from "../ui/Button";
import { useFocusRefusal } from "../ui/Field";
import { Alert, Status } from "../ui/Page";
import {
  commentsOn,
  sendComments,
  sendUndo,
  type Thread,
  targetCommentsQuery,
  threadsOf,
} from "./queries";

/** The most characters of text that a comment holds, as the server counts them (comment.MaxBodyLength). */
export const maxCommentChars = 10_000;

/** What a thread is on: a node, a connection, a board, or a variable, by its ID. */
export interface CommentTarget {
  type: CommentBody["target_type"];
  id: string;
}

/** A text names nothing, so its names are none. */
const noNames = {
  componentName: () => undefined,
  valueName: () => undefined,
};

/** The names that the derivation of a comment's text asks for, of which it has none. */
const noSegmentNames = { value: () => "", component: () => "" };

/**
 * Returns why the server would take none of value, a comment's body as the
 * editor holds it, or undefined: no character that is not white space, or
 * more than maxCommentChars of text, as the server counts them, those of
 * its texts, without the breaks between its paragraphs and its items.
 */
export function commentProblem(
  t: TFunction,
  value: RichTextValue | undefined,
): string | undefined {
  if (value === undefined) {
    return t("comments.error.empty");
  }
  if (value.doc === undefined) {
    return t("comments.error.long", { max: maxCommentChars });
  }
  let chars = 0;
  let printed = false;
  for (const s of segmentsOf(value.doc, noSegmentNames)) {
    if (s.kind === "text") {
      chars += [...s.value].length;
      printed ||= /[^\p{White_Space}]/u.test(s.value);
    }
  }
  if (chars > maxCommentChars) {
    return t("comments.error.long", { max: maxCommentChars });
  }
  return printed ? undefined : t("comments.error.empty");
}

/** The field of a comment's form that a refusal's location names: its body, wherever in it. */
function bodyField(location: string): string | undefined {
  return /^body\.commands\[\d+\]\.payload\.body(\.|$)/.test(location)
    ? "body"
    : undefined;
}

/**
 * Returns what a form or a button of the threads says of error, the
 * server's refusal of its batch: author_required, of a comment that is
 * another's, in the threads' own words.
 */
function refusalOfComment(error: unknown, t: TFunction) {
  return refusalOf(error, t, bodyField, {
    author_required: t("comments.error.author_required"),
  });
}

/**
 * Reports whether form has the focus, or nothing has (focusIsNowhere), so
 * that a refusal of its own takes the focus to its field, rather than from
 * another form of the threads that the user went on to meanwhile.
 */
function focusHere(form: HTMLElement | null): boolean {
  return (
    form !== null &&
    (form.contains(form.ownerDocument.activeElement) ||
      focusIsNowhere(form.ownerDocument))
  );
}

/**
 * The section of the threads on target, under its heading, which a link to
 * them gives the focus (headingId): a form of a new thread at its top, for
 * a role that comments on a project that is not archived, and the threads,
 * newest first.
 */
export function Comments({
  projectId,
  target,
  headingId,
  level = 3,
}: {
  projectId: string;
  target: CommentTarget;
  /** The ID of the section's heading. */
  headingId?: string;
  /** The level of the section's heading: 3 in a panel, under its title, and 2 on a board's page, under its name. */
  level?: 2 | 3;
}) {
  const { t } = useTranslation();
  const doc = useProjectDocument(projectId);
  const project = useSuspenseQuery(projectQuery(projectId)).data;
  const writable =
    commentsOn(project.role) && project.archived_at === undefined;
  const list = useQuery(targetCommentsQuery(doc, target.id));
  const ownId = useId();
  const titleId = headingId ?? `${ownId}-title`;
  const heading = useRef<HTMLHeadingElement>(null);
  const threads = threadsOf(list.data ?? []);
  const open = threads.filter((thread) => thread.root.resolved !== true).length;
  const Heading = level === 2 ? "h2" : "h3";
  return (
    <section aria-labelledby={titleId} className="space-y-3">
      <Heading
        id={titleId}
        ref={heading}
        tabIndex={-1}
        className="text-lg font-semibold"
      >
        {t("comments.title")}
      </Heading>
      {list.data !== undefined && (
        <p className="text-fg-muted">
          {t("comments.counts", {
            open,
            resolved: threads.length - open,
          })}
        </p>
      )}
      {writable && (
        <NewThread doc={doc} target={target} label={t("comments.new")} />
      )}
      {!writable && (
        <p className="text-fg-muted">
          {project.archived_at !== undefined
            ? t("comments.read_only_archived")
            : t("comments.read_only")}
        </p>
      )}
      {list.isPending ? (
        <p className="text-fg-muted">{t("comments.reading")}</p>
      ) : (
        <>
          {list.isError && (
            <ReadFailed
              error={list.error}
              onRetry={() => void list.refetch()}
            />
          )}
          {list.data !== undefined && (
            <ThreadList
              doc={doc}
              threads={threads}
              writable={writable}
              readAt={list.dataUpdatedAt}
              reread={() => list.refetch()}
              heading={heading}
              empty={t("comments.none")}
            />
          )}
        </>
      )}
    </section>
  );
}

/** Says that the threads could not be read, and why, with what reads them again. */
export function ReadFailed({
  error,
  onRetry,
}: {
  error: unknown;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="space-y-2">
      <Alert
        message={[t("comments.read_failed"), refusalOf(error, t)?.message]
          .filter((m) => m !== undefined)
          .join(" ")}
      />
      <Button onClick={onRetry}>{t("comments.read_again")}</Button>
    </div>
  );
}

/** The form of a new thread on target, which empties once its comment is posted. */
function NewThread({
  doc,
  target,
  label,
}: {
  doc: ProjectDocument;
  target: CommentTarget;
  label: string;
}) {
  const { t } = useTranslation();
  const [said, setSaid] = useState<string>();
  return (
    <div className="space-y-2">
      <CommentForm
        label={label}
        hint={t("comments.new_hint")}
        submit={t("comments.post")}
        onEdit={() => setSaid(undefined)}
        onSend={async (body) => {
          await sendComments(doc, [
            {
              type: "comment.create",
              payload: { id: newId("comment"), target: target.id, body },
            },
          ]);
          setSaid(t("comments.posted"));
        }}
      />
      <Status message={said} />
    </div>
  );
}

/** A comment that the user deleted here, which shows in its place with its own Undo, the undo that the server answered its deletion with. */
interface Deleted {
  comment: CommentBody;
  undo: UndoCommandBody[];
  /** When it was deleted, in milliseconds since the epoch, after which a read of the threads tells whether it is still. */
  at: number;
}

/**
 * Returns threads with the comments of deleted that the server no longer
 * lists, each where it was, so that its Undo stays in its place: a reply
 * in its thread, by its ID, which orders replies by when they were posted,
 * and a first comment of a thread that the server no longer lists, as one
 * whose replies were all deleted, as a thread of its own, by its ID, which
 * orders threads newest first.
 */
function withDeleted(
  threads: readonly Thread[],
  deleted: ReadonlyMap<string, Deleted>,
): Thread[] {
  const out = threads.map((thread) => ({
    root: thread.root,
    replies: [...thread.replies],
  }));
  const listed = new Set(
    out.flatMap((thread) => [
      thread.root.id,
      ...thread.replies.map((r) => r.id),
    ]),
  );
  for (const { comment } of deleted.values()) {
    if (listed.has(comment.id)) {
      continue;
    }
    if (comment.parent_id === undefined) {
      out.push({ root: comment, replies: [] });
      continue;
    }
    const thread = out.find((th) => th.root.id === comment.thread_id);
    if (thread !== undefined) {
      thread.replies.push(comment);
      thread.replies.sort((a, b) => compareIds(a.id, b.id));
    }
  }
  return out.sort((a, b) => compareIds(b.root.id, a.root.id));
}

/** Compares two IDs of UUIDv7, whose order is that of when they were made. */
function compareIds(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/**
 * Gives the element of an ID the focus once it shows, as the control that
 * takes the place of one pressed, as a deleted comment's Undo that of its
 * Delete: from where the press left it, the control pressed, or nowhere, as
 * once it went with what it did, but not from where the user has put it
 * meanwhile. It returns what asks for it.
 */
function useFocusOnceShown(): (pressed: Element | null, id: string) => void {
  const [asked, setAsked] = useState<{
    pressed: Element | null;
    id: string;
  }>();
  useEffect(() => {
    if (asked === undefined) {
      return;
    }
    const element = document.getElementById(asked.id);
    if (element === null) {
      return;
    }
    setAsked(undefined);
    if (document.activeElement === asked.pressed || focusIsNowhere(document)) {
      element.focus();
    }
  });
  return (pressed, id) => setAsked({ pressed, id });
}

/**
 * The threads, each under the target that targetOf names, if given, as the
 * project's list does, or empty where there are none. reread reads them
 * again, as once a comment's undo has restored it, after which the list
 * shows it where its Undo was.
 */
export function ThreadList({
  doc,
  threads,
  writable,
  readAt,
  reread,
  heading,
  empty,
  targetOf,
}: {
  doc: ProjectDocument;
  threads: readonly Thread[];
  /** Whether the user starts, answers, and resolves threads, and edits and deletes their own comments. */
  writable: boolean;
  /** When the threads were read, in milliseconds since the epoch. */
  readAt: number;
  reread: () => Promise<unknown>;
  /** The heading of the threads, which takes the focus once a press of a thread leaves it nowhere. */
  heading: RefObject<HTMLElement | null>;
  /** What the list says where it has no thread. */
  empty: string;
  /** What names the target of a thread, as a link to it, where the list is of several targets. */
  targetOf?: (root: CommentBody) => ReactNode;
}) {
  const { t } = useTranslation();
  const userId = useQuery(sessionQuery).data?.user_id;
  const ids = useId();
  const [deleted, setDeleted] = useState<ReadonlyMap<string, Deleted>>(
    () => new Map(),
  );
  const [said, setSaid] = useState<string>();
  // What failed of a thread's press that has no field, as Resolve, Delete,
  // or Undo, which the thread says under its buttons, by the thread's ID.
  const [failed, setFailed] = useState<{ thread: string; error: unknown }>();
  const focusOnceShown = useFocusOnceShown();
  // A press of a thread's button, as a request of the page's, which the
  // app's query client records once it fails, but for a refusal of the
  // server's, which the thread says.
  const press = useMutation({
    mutationFn: (run: () => Promise<void>) => run(),
  });
  const act = async (thread: string, run: () => Promise<void>) => {
    setSaid(undefined);
    setFailed(undefined);
    try {
      await press.mutateAsync(run);
    } catch (error) {
      setFailed({ thread, error });
    }
  };
  // A comment that the list shows again, not deleted, once it was read
  // after the deletion, as once another session of the user's restored it,
  // no longer offers its Undo.
  const restored = [...deleted.values()]
    .filter((d) => d.at < readAt)
    .map((d) => d.comment.id)
    .filter((id) =>
      threads.some(
        (thread) =>
          (thread.root.id === id && !thread.root.deleted) ||
          thread.replies.some((r) => r.id === id),
      ),
    );
  if (restored.length > 0 && !press.isPending) {
    const next = new Map(deleted);
    for (const id of restored) {
      next.delete(id);
    }
    setDeleted(next);
  }
  const shown = withDeleted(threads, deleted);
  const idOf = (what: string, commentId: string) =>
    `${ids}-${what}-${commentId}`;
  const remove = (thread: string, c: CommentBody) =>
    act(thread, async () => {
      const pressed = document.activeElement;
      const undo = await sendComments(doc, [
        { type: "comment.delete", payload: { id: c.id } },
      ]);
      setDeleted((was) =>
        new Map(was).set(c.id, { comment: c, undo, at: Date.now() }),
      );
      setSaid(t("comments.deleted_status"));
      focusOnceShown(pressed, idOf("undo", c.id));
    });
  const restore = (thread: string, d: Deleted) =>
    act(thread, async () => {
      const pressed = document.activeElement;
      await sendUndo(doc, d.undo);
      await reread();
      setDeleted((was) => {
        const next = new Map(was);
        next.delete(d.comment.id);
        return next;
      });
      setSaid(t("comments.restored"));
      focusOnceShown(pressed, idOf("comment", d.comment.id));
    });
  const resolve = (root: CommentBody, resolved: boolean) =>
    act(root.id, async () => {
      await sendComments(doc, [
        {
          type: resolved ? "comment.resolve" : "comment.unresolve",
          payload: { id: root.id },
        },
      ]);
      setSaid(resolved ? t("comments.resolved") : t("comments.reopened"));
    });
  return (
    <div className="space-y-3">
      <Status message={said} />
      {shown.length === 0 ? (
        <p className="text-fg-muted">{empty}</p>
      ) : (
        <ol className="space-y-3">
          {shown.map((thread) => (
            <ThreadItem
              key={thread.root.id}
              doc={doc}
              thread={thread}
              writable={writable}
              userId={userId}
              busy={press.isPending}
              deleted={deleted}
              idOf={idOf}
              failed={
                failed?.thread === thread.root.id ? failed.error : undefined
              }
              target={targetOf?.(thread.root)}
              heading={heading}
              onDelete={(c) => void remove(thread.root.id, c)}
              onUndo={(d) => void restore(thread.root.id, d)}
              onResolve={(resolved) => void resolve(thread.root, resolved)}
            />
          ))}
        </ol>
      )}
    </div>
  );
}

/** A thread: what it is on, where the list names it, its comments, and its buttons. */
function ThreadItem({
  doc,
  thread,
  writable,
  userId,
  busy,
  deleted,
  idOf,
  failed,
  target,
  heading,
  onDelete,
  onUndo,
  onResolve,
}: {
  doc: ProjectDocument;
  thread: Thread;
  writable: boolean;
  userId: string | undefined;
  /** Whether a press of the threads is under way, as a deletion. */
  busy: boolean;
  deleted: ReadonlyMap<string, Deleted>;
  idOf: (what: string, commentId: string) => string;
  /** What the thread's last press without a field failed with, if it did. */
  failed: unknown;
  target: ReactNode;
  heading: RefObject<HTMLElement | null>;
  onDelete: (c: CommentBody) => void;
  onUndo: (d: Deleted) => void;
  onResolve: (resolved: boolean) => void;
}) {
  const { t, i18n } = useTranslation();
  const { root, replies } = thread;
  const [replying, setReplying] = useState(false);
  const replyButton = useRef<HTMLButtonElement>(null);
  const resolved = root.resolved === true;
  const refusal = refusalOfComment(failed, t);
  // A thread whose first comment is gone, of which only a deletion's Undo
  // stays, takes no reply, nor a resolve, of a thread that the server lists
  // no more.
  const live =
    !deleted.has(root.id) ||
    replies.some((r) => !deleted.has(r.id) && !r.deleted);
  const comment = (c: CommentBody, reply: boolean) => (
    <CommentItem
      key={c.id}
      doc={doc}
      comment={c}
      reply={reply}
      own={writable && c.author !== undefined && c.author.id === userId}
      deleted={deleted.get(c.id)}
      busy={busy}
      idOf={idOf}
      onDelete={() => onDelete(c)}
      onUndo={onUndo}
    />
  );
  return (
    <li
      className={`space-y-3 rounded-md border p-3 ${resolved ? "border-border bg-surface" : "border-border"}`}
      data-thread={root.id}
    >
      {target !== undefined && (
        <p className="font-medium break-words">{target}</p>
      )}
      {comment(root, false)}
      {replies.length > 0 && (
        <ol
          aria-label={t("comments.replies", { count: replies.length })}
          className="space-y-3 border-s-2 border-border ps-3"
        >
          {replies.map((r) => (
            <li key={r.id}>{comment(r, true)}</li>
          ))}
        </ol>
      )}
      {resolved && (
        <p className="text-sm text-fg-muted">
          {root.resolved_by === undefined
            ? t("comments.resolved_at", {
                at: formatRelativeTime(
                  i18n.language,
                  new Date(root.resolved_at ?? root.updated_at),
                ),
              })
            : t("comments.resolved_by", {
                name: root.resolved_by.display_name,
                at: formatRelativeTime(
                  i18n.language,
                  new Date(root.resolved_at ?? root.updated_at),
                ),
              })}
        </p>
      )}
      {writable &&
        live &&
        (replying ? (
          <CommentForm
            label={t("comments.reply_label")}
            submit={t("comments.reply_post")}
            autoFocus
            onCancel={() => {
              setReplying(false);
              requestAnimationFrame(() => replyButton.current?.focus());
            }}
            onSend={(body) =>
              sendComments(doc, [
                {
                  type: "comment.create",
                  payload: { id: newId("comment"), parent_id: root.id, body },
                },
              ]).then(() => undefined)
            }
            // The Reply button comes back as the form goes, which takes the
            // focus that the form had, rather than from where the user put
            // it meanwhile.
            onDone={(focused) => {
              setReplying(false);
              if (focused) {
                requestAnimationFrame(() =>
                  (replyButton.current ?? heading.current)?.focus(),
                );
              }
            }}
          />
        ) : (
          <div className="flex flex-wrap gap-2">
            <Button
              ref={replyButton}
              aria-describedby={idOf("byline", root.id)}
              onClick={() => setReplying(true)}
            >
              {t("comments.reply")}
            </Button>
            <LastingButton
              unavailable={busy}
              aria-describedby={idOf("byline", root.id)}
              onClick={() => onResolve(!resolved)}
            >
              {resolved ? t("comments.reopen") : t("comments.resolve")}
            </LastingButton>
          </div>
        ))}
      <Alert message={refusal?.message} />
    </li>
  );
}

/** A comment: who wrote it and when, its body, and the user's own buttons of it, or, deleted, that it is, with its Undo where the user deleted it here. */
function CommentItem({
  doc,
  comment: c,
  reply,
  own,
  deleted,
  busy,
  idOf,
  onDelete,
  onUndo,
}: {
  doc: ProjectDocument;
  comment: CommentBody;
  reply: boolean;
  /** Whether the comment is the user's own, which they edit and delete. */
  own: boolean;
  /** The deletion of the comment that the user made here, whose Undo it offers. */
  deleted: Deleted | undefined;
  busy: boolean;
  idOf: (what: string, commentId: string) => string;
  onDelete: () => void;
  onUndo: (d: Deleted) => void;
}) {
  const { t, i18n } = useTranslation();
  const [editing, setEditing] = useState(false);
  const editButton = useRef<HTMLButtonElement>(null);
  const bylineId = idOf("byline", c.id);
  if (deleted !== undefined || c.deleted || c.body === undefined) {
    return (
      <div
        id={idOf("comment", c.id)}
        tabIndex={-1}
        className="flex flex-wrap items-center gap-2"
      >
        <p id={bylineId} className="text-fg-muted italic">
          {reply ? t("comments.reply_deleted") : t("comments.deleted")}
        </p>
        {deleted !== undefined && (
          <LastingButton
            id={idOf("undo", c.id)}
            unavailable={busy}
            aria-label={t("comments.undo_delete")}
            onClick={() => onUndo(deleted)}
          >
            {t("comments.undo")}
          </LastingButton>
        )}
      </div>
    );
  }
  const posted = new Date(c.created_at);
  const name = c.author?.display_name ?? t("comments.no_author");
  return (
    <div id={idOf("comment", c.id)} tabIndex={-1} className="space-y-2">
      <p id={bylineId} className="text-sm">
        <span className="font-medium">
          {reply
            ? t("comments.reply_byline", { name })
            : t("comments.byline", { name })}
        </span>{" "}
        <time
          dateTime={c.created_at}
          title={formatDateTime(i18n.language, posted)}
          className="text-fg-muted"
        >
          {formatRelativeTime(i18n.language, posted)}
        </time>
        {c.edited_at !== undefined && (
          <span className="text-fg-muted"> {t("comments.edited")}</span>
        )}
      </p>
      {editing ? (
        <CommentForm
          label={t("comments.edit_label")}
          submit={t("comments.save")}
          initial={c.body}
          autoFocus
          onCancel={() => {
            setEditing(false);
            requestAnimationFrame(() => editButton.current?.focus());
          }}
          onSend={(body) =>
            sendComments(doc, [
              { type: "comment.edit", payload: { id: c.id, body } },
            ]).then(() => undefined)
          }
          // Edit comes back as the form goes, which takes the focus that the
          // form had, rather than from where the user put it meanwhile.
          onDone={(focused) => {
            setEditing(false);
            if (focused) {
              requestAnimationFrame(() => editButton.current?.focus());
            }
          }}
        />
      ) : (
        <>
          <div className="break-words">
            <RichText doc={c.body} names={noNames} />
          </div>
          {own && (
            <div className="flex flex-wrap gap-2">
              <Button
                ref={editButton}
                aria-describedby={bylineId}
                onClick={() => setEditing(true)}
              >
                {t("comments.edit")}
              </Button>
              <LastingButton
                unavailable={busy}
                aria-describedby={bylineId}
                onClick={onDelete}
              >
                {t("comments.delete")}
              </LastingButton>
            </div>
          )}
        </>
      )}
    </div>
  );
}

/**
 * The form of a comment's body: a rich text of formatting and links alone,
 * which the server takes, with why it would not under the field, the
 * server's refusal among them, as author_required of another's comment,
 * and its button, which says that it is unavailable while its batch is
 * under way, rather than be disabled, so that it keeps the focus, which it
 * has once the form has posted, the text emptied, or which the form that
 * closes once it has, a reply's or an edit's, gives where onDone says.
 */
function CommentForm({
  label,
  hint,
  submit,
  initial,
  autoFocus = false,
  onEdit,
  onSend,
  onDone,
  onCancel,
}: {
  label: string;
  hint?: string;
  /** The submit button's label. */
  submit: string;
  /** The body that the form starts with, as an edit's. */
  initial?: RichTextDoc;
  /** Whether the text takes the focus as the form shows, as a reply's and an edit's, which their button opened. */
  autoFocus?: boolean;
  /** Called as the text changes. */
  onEdit?: () => void;
  /** Sends the body, and rejects with the server's refusal. */
  onSend: (body: RichTextDoc) => Promise<void>;
  /**
   * Closes the form once its body is sent, told whether the form had the
   * focus, or nothing had (focusHere), so that what takes its place takes
   * it; a form that stays, as a new thread's, has none, and is emptied.
   */
  onDone?: (focused: boolean) => void;
  /** Closes the form unsent, which a form that stays has none of. */
  onCancel?: () => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const [value, setValue] = useState<RichTextValue | undefined>(
    initial === undefined ? undefined : { doc: initial },
  );
  const [problem, setProblem] = useState<string>();
  // The editor, made anew, empty, once its comment is posted.
  const [posted, setPosted] = useState(0);
  const send = useMutation({ mutationFn: onSend });
  const refusal = refusalOfComment(send.error, t);
  useFocusRefusal(form, send.error, focusHere);
  useEffect(() => {
    if (autoFocus) {
      form.current?.querySelector<HTMLElement>('[role="textbox"]')?.focus();
    }
  }, [autoFocus]);
  const onSubmit = async (event: FormEvent) => {
    event.preventDefault();
    if (send.isPending) {
      return;
    }
    const p = commentProblem(t, value);
    setProblem(p);
    if (p !== undefined || value?.doc === undefined) {
      form.current?.querySelector<HTMLElement>('[role="textbox"]')?.focus();
      return;
    }
    try {
      await send.mutateAsync(value.doc);
    } catch {
      // The form says why under its field (refusal).
      return;
    }
    if (onDone !== undefined) {
      onDone(focusHere(form.current));
      return;
    }
    send.reset();
    setValue(undefined);
    setPosted((n) => n + 1);
  };
  return (
    <form ref={form} onSubmit={onSubmit} className="space-y-2" noValidate>
      <RichTextEditor
        key={posted}
        label={label}
        hint={hint}
        references={false}
        defaultValue={initial}
        error={problem ?? refusal?.fields.body ?? refusal?.message}
        onChange={(next) => {
          setValue(next);
          setProblem(undefined);
          onEdit?.();
          if (send.isError) {
            send.reset();
          }
        }}
      />
      <div className="flex flex-wrap gap-2">
        <LastingButton
          type="submit"
          variant="primary"
          unavailable={send.isPending}
        >
          {submit}
        </LastingButton>
        {onCancel !== undefined && (
          <Button disabled={send.isPending} onClick={onCancel}>
            {t("comments.cancel")}
          </Button>
        )}
      </div>
    </form>
  );
}
