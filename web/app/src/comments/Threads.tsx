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
// are no steps of the page's undo (sendComments): a deleted comment offers an
// Undo of its own where it was deleted, a line of its thread, or of the
// threads where the server no longer lists the thread, which sends the undo
// that the server answered its deletion with, until the next batch that the
// threads send, their new thread's too, or until the list goes, whatever the
// threads read meanwhile (Deleted), while the threads show the comments as
// the server lists them. Each action expects the revision of what it changes
// that the list showed, or, of an edit, that the comment had as its field
// opened, so that it is refused for a change made elsewhere since, which
// the list says, read again, rather than write over it (PRD §7.10). A first
// comment deleted while its thread has replies shows as deleted, without
// its body and its author, as the server lists it.

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
  createContext,
  type FormEvent,
  type ReactNode,
  type RefObject,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { refusalOf } from "../auth/refusal";
import { hasPrintedIn } from "../canvas/edit";
import { useProjectDocument } from "../document/react";
import type { Command, ProjectDocument } from "../document/store";
import { conflictOf, projectQuery } from "../project/queries";
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

/**
 * What a page that would take a comment's form away, as the board's page
 * that follows play's flow to another board, is told of each form of the
 * threads under it that holds what the user typed, unsent: an edit's or a
 * reply's while it is open, and a new thread's with text in it. Each such
 * form calls it as it begins to hold it, and what it returns as it no
 * longer does, so that the page waits for none to hold any (plan item 74).
 */
export const CommentDrafts = createContext<(() => () => void) | undefined>(
  undefined,
);

/**
 * Returns how many forms of the threads under the CommentDrafts that it
 * gives hold what the user typed, unsent, and what it gives them, which
 * the board's page counts of its threads, its own under the canvas and
 * those of the canvas's inspector (plan item 74).
 */
export function useCommentDrafts(): [number, () => () => void] {
  const [drafts, setDrafts] = useState(0);
  const hold = useCallback(() => {
    setDrafts((n) => n + 1);
    let held = true;
    return () => {
      if (held) {
        held = false;
        setDrafts((n) => n - 1);
      }
    };
  }, []);
  return [drafts, hold];
}

/** A text names nothing, so its names are none. */
const noNames = {
  componentName: () => undefined,
  valueName: () => undefined,
};

/** The names that the derivation of a comment's text asks for, of which it has none. */
const noSegmentNames = { value: () => "", component: () => "" };

/**
 * Returns how many characters of text value, a comment's body as the
 * editor holds it, has, as the server counts them, those of its texts,
 * without the breaks between its paragraphs and its items, and whether one
 * of them is printed, as the server tells it (input.Printed, hasPrintedIn):
 * of a text too long for the editor to give a document of, more than
 * maxCommentChars, and printed.
 */
function textOf(value: RichTextValue | undefined): {
  chars: number;
  printed: boolean;
} {
  if (value === undefined) {
    return { chars: 0, printed: false };
  }
  if (value.doc === undefined) {
    return { chars: maxCommentChars + 1, printed: true };
  }
  let chars = 0;
  let printed = false;
  for (const s of segmentsOf(value.doc, noSegmentNames)) {
    if (s.kind === "text") {
      chars += [...s.value].length;
      printed ||= hasPrintedIn(s.value);
    }
  }
  return { chars, printed };
}

/**
 * Returns why the server would take none of value, a comment's body as the
 * editor holds it, or undefined: no character that is printed, as one of
 * white space or of U+200B ZERO WIDTH SPACE alone, or more than
 * maxCommentChars of text, as the server counts them (textOf).
 */
export function commentProblem(
  t: TFunction,
  value: RichTextValue | undefined,
): string | undefined {
  const { chars, printed } = textOf(value);
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
 * server's refusal of its batch, in the threads' own words: author_required,
 * of a comment that is another's, and revision_conflict, of a comment that
 * changed elsewhere since the list showed it, or since its edit's field
 * opened, as changed, which a form says keeping what was typed.
 */
function refusalOfComment(error: unknown, t: TFunction, form: boolean) {
  return refusalOf(error, t, bodyField, {
    author_required: t("comments.error.author_required"),
    revision_conflict: form
      ? t("comments.error.changed_edit")
      : t("comments.error.changed"),
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
  // The batches of the new thread's form, each of which ends the Undo that
  // the threads show, as theirs do (ThreadList's sent).
  const [sent, setSent] = useState(0);
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
        // Made anew for another target, as a section that stays shows
        // another's threads, so that what was typed for one is never posted
        // on the other.
        <NewThread
          key={target.id}
          doc={doc}
          target={target}
          label={t("comments.new")}
          onSending={() => setSent((n) => n + 1)}
        />
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
              reread={() => list.refetch()}
              heading={heading}
              empty={t("comments.none")}
              sent={sent}
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

/**
 * The form of a new thread on target, which empties once its comment is
 * posted. The comment's ID is the form's until then, made anew once it is
 * posted, so that Post pressed again once no answer came sends the same
 * batch, which the document sends with the IDs of the first (enqueue), and
 * the server answers as it did that one rather than post the comment twice,
 * as the dialogs that create keep theirs.
 */
function NewThread({
  doc,
  target,
  label,
  onSending,
}: {
  doc: ProjectDocument;
  target: CommentTarget;
  label: string;
  /** Called as the form sends its batch, a next action of the threads. */
  onSending: () => void;
}) {
  const { t } = useTranslation();
  const [said, setSaid] = useState<string>();
  const [id, setId] = useState(() => newId("comment"));
  return (
    <div className="space-y-2">
      <CommentForm
        label={label}
        hint={t("comments.new_hint")}
        submit={t("comments.post")}
        onEdit={() => setSaid(undefined)}
        onSend={async (body) => {
          onSending();
          await sendComments(doc, [
            {
              type: "comment.create",
              payload: { id, target: target.id, body },
            },
          ]);
          setId(newId("comment"));
          setSaid(t("comments.posted"));
        }}
      />
      <Status message={said} />
    </div>
  );
}

/**
 * The comment that the user deleted last in a list of threads, whose Undo,
 * the undo that the server answered its deletion with, is a line of its
 * thread, or of the threads where the server no longer lists the thread,
 * as once it was the thread's last comment that was not deleted, or once a
 * filter keeps the thread no more, until the next batch that the threads
 * send, the list's or their new thread's, or until the list goes: the
 * Undo's own ends it once it applies, or once the server refuses it for a
 * change made elsewhere since. No read of the threads ends it, as one that
 * was under way as the deletion was sent may be answered after it with the
 * comment as it was, which the threads then show, as the server listed it.
 */
interface Deleted {
  comment: CommentBody;
  undo: UndoCommandBody[];
}

/** Sends a batch of the list's commands of comments, as sendComments does, which the list knows of (ThreadList's send). */
type Send = (commands: Command[]) => Promise<UndoCommandBody[]>;

/** The line of a deletion's Undo (Deleted), which says what was deleted, with the Undo. */
function UndoLine({
  deleted,
  id,
  busy,
  onUndo,
}: {
  deleted: Deleted;
  /** The Undo's ID, by which it takes the focus that Delete had. */
  id: string;
  busy: boolean;
  onUndo: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap items-center gap-2">
      <p className="text-fg-muted italic">
        {deleted.comment.parent_id === undefined
          ? t("comments.deleted_line")
          : t("comments.reply_deleted_line")}
      </p>
      <LastingButton
        id={id}
        unavailable={busy}
        aria-label={t("comments.undo_delete")}
        onClick={onUndo}
      >
        {t("comments.undo")}
      </LastingButton>
    </div>
  );
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
 * project's list does, or empty where there are none, as the server lists
 * them, with the line of the Undo of the deletion that the user made here
 * last (Deleted). reread reads them again, as once a comment's undo has
 * restored it, after which the list shows it, and once the server refused
 * an action for a change made elsewhere, which the list then shows.
 */
export function ThreadList({
  doc,
  threads,
  writable,
  reread,
  heading,
  empty,
  targetOf,
  sent = 0,
}: {
  doc: ProjectDocument;
  threads: readonly Thread[];
  /** Whether the user starts, answers, and resolves threads, and edits and deletes their own comments. */
  writable: boolean;
  reread: () => Promise<unknown>;
  /** The heading of the threads, which takes the focus once a press of a thread leaves it nowhere. */
  heading: RefObject<HTMLElement | null>;
  /** What the list says where it has no thread. */
  empty: string;
  /** What names the target of a thread, as a link to it, where the list is of several targets. */
  targetOf?: (root: CommentBody) => ReactNode;
  /** Counts the batches that the threads send outside the list, as their new thread's, each of which ends the Undo shown, as the list's own do (Deleted). */
  sent?: number;
}) {
  const { t } = useTranslation();
  const userId = useQuery(sessionQuery).data?.user_id;
  const ids = useId();
  const [deleted, setDeleted] = useState<Deleted>();
  // A batch of the threads' outside the list ends the Undo shown, as the
  // list's own do (send).
  const [sentSeen, setSentSeen] = useState(sent);
  if (sent !== sentSeen) {
    setSentSeen(sent);
    setDeleted(undefined);
  }
  const [said, setSaid] = useState<string>();
  // What failed of a thread's press that has no field, as Resolve, Delete,
  // or Undo, which the thread says under its buttons, by the thread's ID, or
  // the threads above them where the list no longer shows the thread, and
  // whether it was an Undo's, whose refusal for a change made elsewhere
  // since the deletion says that the deletion is no longer undone here, as
  // the Undo ends then.
  const [failed, setFailed] = useState<{
    thread: string;
    error: unknown;
    undo?: boolean;
  }>();
  const focusOnceShown = useFocusOnceShown();
  // A press of a thread's button, as a request of the page's, which the
  // app's query client records once it fails, but for a refusal of the
  // server's, which the thread says.
  const press = useMutation({
    mutationFn: (run: () => Promise<void>) => run(),
  });
  const act = async (
    thread: string,
    run: () => Promise<void>,
    undo = false,
  ) => {
    setSaid(undefined);
    setFailed(undefined);
    try {
      await press.mutateAsync(run);
    } catch (error) {
      setFailed({ thread, error, undo });
    }
  };
  // What a press failed with, in the threads' own words (refusalOfComment).
  const failure =
    failed === undefined
      ? undefined
      : failed.undo === true && conflictOf(failed.error)
        ? t("comments.error.changed_undo")
        : refusalOfComment(failed.error, t, false)?.message;
  // Reads the threads again once the server refused a batch for a change
  // made elsewhere since the list showed what it changes, so that the list
  // shows it as it is, and an edit saved again expects it (useExpected),
  // before the refusal is said.
  const rereadAfter = async (error: unknown) => {
    if (conflictOf(error)) {
      await reread().catch(() => undefined);
    }
  };
  // Every batch of the list's but an Undo's ends the Undo shown, as the
  // list's next action (Deleted).
  const send: Send = async (commands) => {
    setDeleted(undefined);
    try {
      return await sendComments(doc, commands);
    } catch (error) {
      await rereadAfter(error);
      throw error;
    }
  };
  const idOf = (what: string, commentId: string) =>
    `${ids}-${what}-${commentId}`;
  // The thread whose line the Undo shown is, where the list shows it, or
  // else none, of which the threads' own line is (Deleted).
  const undoThread =
    deleted !== undefined &&
    threads.some((th) => th.root.id === deleted.comment.thread_id)
      ? deleted.comment.thread_id
      : undefined;
  // The Undo, which takes the focus that Delete had, keeps it as it moves
  // between its thread's line and the threads' own, as a read shows the
  // thread no more, or again: the Undo that it leaves takes the focus with
  // it, which the browser leaves nowhere then (useFocusOnceShown).
  const undoPlace = useRef<{ id: string; thread: string | undefined }>(
    undefined,
  );
  useEffect(() => {
    const was = undoPlace.current;
    undoPlace.current =
      deleted === undefined
        ? undefined
        : { id: deleted.comment.id, thread: undoThread };
    if (
      deleted !== undefined &&
      was?.id === deleted.comment.id &&
      was.thread !== undoThread
    ) {
      focusOnceShown(null, idOf("undo", deleted.comment.id));
    }
  });
  const remove = (thread: Thread, c: CommentBody) =>
    act(thread.root.id, async () => {
      const pressed = document.activeElement;
      const undo = await send([
        {
          type: "comment.delete",
          payload: { id: c.id },
          expected_rev: { [c.id]: c.rev },
        },
      ]);
      if (undo.length === 0) {
        // The comment was deleted already, as the server answered, which
        // has nothing to undo: the list shows it gone, without an Undo.
        await reread().catch(() => undefined);
        setSaid(t("comments.deleted"));
        return;
      }
      setDeleted({ comment: c, undo });
      setSaid(t("comments.deleted_status"));
      focusOnceShown(pressed, idOf("undo", c.id));
    });
  // The Undo ends once it applies, or once the server refuses it for a
  // change made elsewhere since the deletion, as the comment's restoration
  // in another session of the user's, which would refuse it again, and
  // which the list, read again, then shows; it stays where it failed
  // otherwise, as where no answer came, to be pressed again. The comment
  // that the list then shows takes the focus that Undo had.
  const restore = (d: Deleted) =>
    act(
      d.comment.thread_id,
      async () => {
        const pressed = document.activeElement;
        const ended = async () => {
          await reread().catch(() => undefined);
          setDeleted(undefined);
          focusOnceShown(pressed, idOf("comment", d.comment.id));
        };
        try {
          await sendUndo(doc, d.undo);
        } catch (error) {
          if (conflictOf(error)) {
            await ended();
          }
          throw error;
        }
        await ended();
        setSaid(t("comments.restored"));
      },
      true,
    );
  const resolve = (root: CommentBody, resolved: boolean) =>
    act(root.id, async () => {
      await send([
        {
          type: resolved ? "comment.resolve" : "comment.unresolve",
          payload: { id: root.id },
          expected_rev: { [root.id]: root.rev },
        },
      ]);
      setSaid(resolved ? t("comments.resolved") : t("comments.reopened"));
    });
  const undoLine = (d: Deleted) => (
    <UndoLine
      deleted={d}
      id={idOf("undo", d.comment.id)}
      busy={press.isPending}
      onUndo={() => void restore(d)}
    />
  );
  // What a press of a thread that the list no longer shows failed with, as
  // the Undo of the deletion of its last comment, which the threads say
  // above them.
  const failedElsewhere =
    failed !== undefined && !threads.some((th) => th.root.id === failed.thread)
      ? failure
      : undefined;
  return (
    <div className="space-y-3">
      <Status message={said} />
      {deleted !== undefined && undoThread === undefined && undoLine(deleted)}
      <Alert message={failedElsewhere} />
      {threads.length === 0 ? (
        <p className="text-fg-muted">{empty}</p>
      ) : (
        <ol className="space-y-3">
          {threads.map((thread) => (
            <ThreadItem
              key={thread.root.id}
              send={send}
              thread={thread}
              writable={writable}
              userId={userId}
              busy={press.isPending}
              idOf={idOf}
              undo={
                deleted !== undefined && undoThread === thread.root.id
                  ? undoLine(deleted)
                  : undefined
              }
              failed={failed?.thread === thread.root.id ? failure : undefined}
              target={targetOf?.(thread.root)}
              heading={heading}
              onDelete={(c) => void remove(thread, c)}
              onResolve={(resolved) => void resolve(thread.root, resolved)}
            />
          ))}
        </ol>
      )}
    </div>
  );
}

/** A thread: what it is on, where the list names it, its comments, the line of the Undo of a deletion of one of them, and its buttons. */
function ThreadItem({
  send,
  thread,
  writable,
  userId,
  busy,
  idOf,
  undo,
  failed,
  target,
  heading,
  onDelete,
  onResolve,
}: {
  send: Send;
  thread: Thread;
  writable: boolean;
  userId: string | undefined;
  /** Whether a press of the threads is under way, as a deletion. */
  busy: boolean;
  idOf: (what: string, commentId: string) => string;
  /** The line of the Undo of the deletion of a comment of the thread that the user made here, if it is shown (Deleted). */
  undo: ReactNode;
  /** What the thread's last press without a field failed with, if it did, in the threads' own words. */
  failed: string | undefined;
  target: ReactNode;
  heading: RefObject<HTMLElement | null>;
  onDelete: (c: CommentBody) => void;
  onResolve: (resolved: boolean) => void;
}) {
  const { t, i18n } = useTranslation();
  const { root, replies } = thread;
  const [replying, setReplying] = useState(false);
  const replyButton = useRef<HTMLButtonElement>(null);
  const resolved = root.resolved === true;
  const comment = (c: CommentBody, reply: boolean) => (
    <CommentItem
      key={c.id}
      send={send}
      comment={c}
      reply={reply}
      own={writable && c.author !== undefined && c.author.id === userId}
      busy={busy}
      idOf={idOf}
      onDelete={() => onDelete(c)}
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
      {undo}
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
        (replying ? (
          <ReplyForm
            send={send}
            rootId={root.id}
            onCancel={() => {
              setReplying(false);
              requestAnimationFrame(() => replyButton.current?.focus());
            }}
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
      <Alert message={failed} />
    </li>
  );
}

/**
 * The form of a reply to the thread whose first comment's ID is rootId,
 * whose comment's ID is the form's while it is open, so that Post reply
 * pressed again once no answer came sends the same batch, which the server
 * answers as it did the first rather than post the reply twice (NewThread).
 */
function ReplyForm({
  send,
  rootId,
  onCancel,
  onDone,
}: {
  send: Send;
  rootId: string;
  onCancel: () => void;
  onDone: (focused: boolean) => void;
}) {
  const { t } = useTranslation();
  const [id] = useState(() => newId("comment"));
  return (
    <CommentForm
      label={t("comments.reply_label")}
      submit={t("comments.reply_post")}
      autoFocus
      onCancel={onCancel}
      onSend={(body) =>
        send([
          {
            type: "comment.create",
            payload: { id, parent_id: rootId, body },
          },
        ]).then(() => undefined)
      }
      onDone={onDone}
    />
  );
}

/** A comment: who wrote it and when, its body, and the user's own buttons of it, or, deleted, that it is, as the server lists a thread's first comment that is deleted while a reply of it is not. */
function CommentItem({
  send,
  comment: c,
  reply,
  own,
  busy,
  idOf,
  onDelete,
}: {
  send: Send;
  comment: CommentBody;
  reply: boolean;
  /** Whether the comment is the user's own, which they edit and delete. */
  own: boolean;
  busy: boolean;
  idOf: (what: string, commentId: string) => string;
  onDelete: () => void;
}) {
  const { t, i18n } = useTranslation();
  const [editing, setEditing] = useState(false);
  const editButton = useRef<HTMLButtonElement>(null);
  const bylineId = idOf("byline", c.id);
  if (c.deleted || c.body === undefined) {
    return (
      <div id={idOf("comment", c.id)} tabIndex={-1}>
        <p id={bylineId} className="text-fg-muted italic">
          {reply ? t("comments.reply_deleted") : t("comments.deleted")}
        </p>
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
        <EditForm
          comment={c}
          body={c.body}
          send={send}
          onCancel={() => {
            setEditing(false);
            requestAnimationFrame(() => editButton.current?.focus());
          }}
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
 * Returns the revision that the comment of id has now, as error, the
 * server's refusal of a batch for a revision that changed elsewhere, says
 * of it (revision_conflict's current_rev), or undefined for another error,
 * or a comment that it says is gone.
 */
function currentRev(error: unknown, id: string): number | undefined {
  if (!conflictOf(error)) {
    return undefined;
  }
  const conflicts = (error.error?.details as { conflicts?: unknown })
    ?.conflicts;
  const conflict = Array.isArray(conflicts)
    ? (conflicts as { id?: unknown; current_rev?: unknown }[]).find(
        (c) => c.id === id,
      )
    : undefined;
  return typeof conflict?.current_rev === "number"
    ? conflict.current_rev
    : undefined;
}

/**
 * The form of an edit of comment, of the body that it had as it opened,
 * which expects the revision that the comment had then, so that an edit
 * made elsewhere since, as the author's in another tab, is refused rather
 * than written over (PRD §7.10), which the form says under its field,
 * keeping what was typed, once the list is read again; an edit saved again
 * then expects the revision that the refusal said the comment has
 * (currentRev), the server's own, rather than the one that the list shows
 * as the refusal is said, as the tree's dialogs take it (useExpected): the
 * list's read may be answered after the refusal, and the field would then
 * expect the revision that it was refused for again.
 */
function EditForm({
  comment: c,
  body,
  send,
  onCancel,
  onDone,
}: {
  comment: CommentBody;
  body: RichTextDoc;
  send: Send;
  onCancel: () => void;
  onDone: (focused: boolean) => void;
}) {
  const { t } = useTranslation();
  const [rev, setRev] = useState(c.rev);
  return (
    <CommentForm
      label={t("comments.edit_label")}
      submit={t("comments.save")}
      initial={body}
      autoFocus
      onCancel={onCancel}
      onSend={async (next) => {
        try {
          await send([
            {
              type: "comment.edit",
              payload: { id: c.id, body: next },
              expected_rev: { [c.id]: rev },
            },
          ]);
        } catch (refused) {
          const now = currentRev(refused, c.id);
          if (now !== undefined) {
            setRev(now);
          }
          throw refused;
        }
      }}
      onDone={onDone}
    />
  );
}

/**
 * The form of a comment's body: a rich text of formatting and links alone,
 * which the server takes, with why it would not under the field, the
 * server's refusal among them, as author_required of another's comment,
 * and its button, which says that it is unavailable while its batch is
 * under way, rather than be disabled, so that it keeps the focus, which it
 * has once the form has posted, the text emptied, or which the form that
 * closes once it has, a reply's or an edit's, gives where onDone says. A
 * reply's or an edit's, while it is open, and a new thread's, with text in
 * it, hold what was typed, which they tell the page of (CommentDrafts).
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
  const refusal = refusalOfComment(send.error, t, true);
  useFocusRefusal(form, send.error, focusHere);
  const hold = useContext(CommentDrafts);
  const holds = onCancel !== undefined || textOf(value).chars > 0;
  useEffect(() => {
    if (holds && hold !== undefined) {
      return hold();
    }
  }, [holds, hold]);
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
