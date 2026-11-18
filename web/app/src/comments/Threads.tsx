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
// its body and its author, as the server lists it. The threads move the
// focus after their own presses alone (usePressFocus): to the Undo that a
// deletion gives, and to their heading once the control that a press left
// the focus on goes. What a form of the threads holds, unsent, a new
// thread's text, or a reply or an edit open, stays with the project's
// document until it is posted or cancelled (draftsOf), so that the form made
// again, as once what is selected changes and changes back, opens with it.

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
import { canonical } from "../document/apply";
import { useProjectDocument } from "../document/react";
import {
  type Command,
  conflictsOf,
  type ProjectDocument,
} from "../document/store";
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
 * What a form of the threads holds of what the user typed, unsent, which
 * the page's document of the project keeps (draftsOf): the body, once the
 * user changed it; of a new comment's form, the ID that it last sent the
 * body under (keptId); and of an edit's, the revision that it expects, the
 * one that its comment had as its field opened, or that a refusal gave
 * since (EditForm).
 */
interface Draft {
  body?: RichTextDoc;
  kept?: { id: string; body: string };
  rev?: number;
}

/** The drafts of the forms of the threads of each document of a project, by their keys (draftsOf). */
const draftsByDocument = new WeakMap<ProjectDocument, Map<string, Draft>>();

/**
 * Returns the drafts of the forms of the threads of doc, the page's
 * document of the project, which goes with the account: a new thread's,
 * while it holds text, by its target's ID, and a reply's and an edit's,
 * while they are open, by their thread's and their comment's (draftKey), as
 * the page counts them as holding what was typed (CommentDrafts), each until
 * it is posted or cancelled, or, of a new thread, emptied, but for the ID
 * that it last sent a body under (keptId), which a new thread's keeps until
 * its comment is posted. A form made
 * again, as once what is selected on the canvas, or the variable whose
 * panel shows, changes and changes back, or once a thread that a read took
 * out of the list is back, opens with what it held, without the focus,
 * which only a press that opens it gives.
 */
function draftsOf(doc: ProjectDocument): Map<string, Draft> {
  let drafts = draftsByDocument.get(doc);
  if (drafts === undefined) {
    drafts = new Map();
    draftsByDocument.set(doc, drafts);
  }
  return drafts;
}

/** The key of a form's draft (draftsOf): a new thread's on the target of id, a reply's to the thread of id, or an edit's of the comment of id. */
function draftKey(form: "thread" | "reply" | "edit", id: string): string {
  return `${form}:${id}`;
}

/** Returns the draft of drafts of key, made anew, empty, if they have none. */
function draftOf(drafts: Map<string, Draft>, key: string): Draft {
  let draft = drafts.get(key);
  if (draft === undefined) {
    draft = {};
    drafts.set(key, draft);
  }
  return draft;
}

/**
 * Returns the ID of the comment that a form posts with body, as draft keeps
 * it: the one that the form last sent that body under, until the form's
 * comment is posted and its draft goes, though its text was emptied and
 * typed again meanwhile, or else one made anew, so that Post
 * pressed again once no answer came, with the text as it was, sends the
 * same batch, which the document sends with the IDs of the first (enqueue),
 * and which the server answers as it did that one rather than post the
 * comment twice, as the dialogs that create keep theirs, though the form
 * was made again meanwhile; and that Post pressed with the text changed
 * since posts a comment of its own, under its own ID, rather than one that
 * the server refuses for the ID of the first, which may have been posted
 * (NewThread, ReplyForm).
 */
function keptId(draft: Draft, body: RichTextDoc): string {
  const sent = JSON.stringify(body);
  if (draft.kept?.body !== sent) {
    draft.kept = { id: newId("comment"), body: sent };
  }
  return draft.kept.id;
}

/**
 * The form of a new thread on target, which empties once its comment is
 * posted, whose ID is the one that the form keeps for its body (keptId),
 * and which opens with what it held, unsent, as it was made last (draftsOf).
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
  const drafts = draftsOf(doc);
  const key = draftKey("thread", target.id);
  const [initial] = useState(() => drafts.get(key)?.body);
  return (
    <div className="space-y-2">
      <CommentForm
        label={label}
        hint={t("comments.new_hint")}
        submit={t("comments.post")}
        initial={initial}
        onEdit={(value) => {
          setSaid(undefined);
          // The form holds what was typed while it has text, as the page
          // counts it (CommentForm's holds); emptied, it keeps the ID that
          // it last sent a body under until its comment is posted, so that
          // the same text typed again once no answer came is the same
          // batch (keptId).
          const draft = drafts.get(key);
          if (textOf(value).chars === 0) {
            if (draft?.kept === undefined) {
              drafts.delete(key);
            } else {
              draft.body = undefined;
            }
          } else if (value?.doc !== undefined) {
            draftOf(drafts, key).body = value.doc;
          }
        }}
        onSend={async (body) => {
          onSending();
          await sendComments(doc, [
            {
              type: "comment.create",
              payload: {
                id: keptId(draftOf(drafts, key), body),
                target: target.id,
                body,
              },
            },
          ]);
          drafts.delete(key);
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
 * send, the list's or their new thread's, which ends what the threads said
 * of it with it, or until the list goes: the Undo's own ends it once it
 * applies, or once the server refuses it for a change made elsewhere
 * since. No read of the threads ends it, as one that
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
 * The focus of the presses of a list of threads, which the list moves
 * after its own presses alone: a press leaves the focus where it was, on
 * the control pressed (pressing), or gives it to the control that takes
 * that one's place once it shows, as a deleted comment's Undo that of its
 * Delete (focusOnceShown), from where the press left it, the control
 * pressed, or nowhere, as once it went with what it did, but not from where
 * the user has put it meanwhile; and once the control that a press left the
 * focus on goes while it has it, as a resolved thread's Reopen out of a
 * list of open threads, read again, a deletion's Undo out of its thread
 * once the server lists the thread no more, or the Undo itself once it has
 * restored the comment, heading, the threads' heading, takes it, until the
 * next press, or until the focus leaves that control. What another
 * session's change takes away otherwise leaves the focus where the browser
 * leaves it.
 */
function usePressFocus(heading: RefObject<HTMLElement | null>): {
  pressing: () => void;
  focusOnceShown: (pressed: Element | null, id: string) => void;
} {
  // The control that the last press left the focus on, while it has it.
  const left = useRef<Element | null>(null);
  const [asked, setAsked] = useState<{
    pressed: Element | null;
    id: string;
  }>();
  useEffect(() => {
    if (asked !== undefined) {
      const element = document.getElementById(asked.id);
      if (element !== null) {
        setAsked(undefined);
        if (
          document.activeElement === asked.pressed ||
          focusIsNowhere(document)
        ) {
          element.focus();
          left.current = element;
        }
      }
    }
    const control = left.current;
    if (control === null) {
      return;
    }
    if (!control.isConnected) {
      left.current = null;
      if (focusIsNowhere(document)) {
        heading.current?.focus();
      }
    } else if (
      document.activeElement !== control &&
      !focusIsNowhere(document)
    ) {
      left.current = null;
    }
  });
  return {
    pressing: () => {
      left.current = focusIsNowhere(document) ? null : document.activeElement;
    },
    focusOnceShown: (pressed, id) => setAsked({ pressed, id }),
  };
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
  /** The heading of the threads, which takes the focus once the control that a press of theirs left it on goes (usePressFocus). */
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
  // What the threads say of their last press, as that a deletion has an
  // Undo, until their next action.
  const [said, setSaid] = useState<string>();
  // What failed of a thread's press that has no field, as Resolve, Delete,
  // or Undo, which the thread says under its buttons, by the thread's ID, or
  // the threads above them where the list no longer shows the thread, and
  // whether it was an Undo's, whose refusal for a change made elsewhere
  // since the deletion says that the deletion is no longer undone here, as
  // the Undo ends then, until the threads' next action.
  const [failed, setFailed] = useState<{
    thread: string;
    error: unknown;
    undo?: boolean;
  }>();
  // A batch of the threads' outside the list, as their new thread's, is
  // their next action, as the list's own are (send): it ends the Undo
  // shown, and what the threads said of their last press.
  const [sentSeen, setSentSeen] = useState(sent);
  if (sent !== sentSeen) {
    setSentSeen(sent);
    setDeleted(undefined);
    setSaid(undefined);
    setFailed(undefined);
  }
  const pressFocus = usePressFocus(heading);
  const drafts = draftsOf(doc);
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
    pressFocus.pressing();
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
  // Every batch of the list's but an Undo's, a press's or a form's, as a
  // reply's or an edit's, is the threads' next action, which ends the Undo
  // shown (Deleted), and what the threads said of their last press, or its
  // failure, as a press's own start does (act), so that neither says what
  // is no longer so, as that an Undo that has gone brings the comment back.
  const send: Send = async (commands) => {
    setDeleted(undefined);
    setSaid(undefined);
    setFailed(undefined);
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
  // The Undo takes the focus that Delete had (usePressFocus), which the
  // threads' heading takes once it goes, as from its thread's line once
  // the server lists the thread no more. The press ends once the list is
  // read again, as the Undo's does, so that the list no longer shows the
  // comment, whose Delete, pressed again before, as a double click presses
  // it, would be the threads' next batch, which ends the Undo, and would
  // change nothing, as the comment is deleted already, so that nothing
  // would offer to bring the comment back.
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
      pressFocus.focusOnceShown(pressed, idOf("undo", c.id));
      await reread().catch(() => undefined);
    });
  // The Undo ends once it applies, or once the server refuses it for a
  // change made elsewhere since the deletion, as the comment's restoration
  // in another session of the user's, which would refuse it again, and
  // which the list, read again, then shows; it stays where it failed
  // otherwise, as where no answer came, to be pressed again. The threads'
  // heading takes the focus that it had as it ends (usePressFocus).
  const restore = (d: Deleted) =>
    act(
      d.comment.thread_id,
      async () => {
        const ended = async () => {
          await reread().catch(() => undefined);
          setDeleted(undefined);
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
              drafts={drafts}
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
  drafts,
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
  /** The drafts of the forms of the threads, which a reply's and an edit's open with (draftsOf). */
  drafts: Map<string, Draft>;
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
  // The reply's form is open while its draft is (draftsOf), as it was made
  // last; Reply's press alone gives its field the focus (opened).
  const replyKey = draftKey("reply", root.id);
  const [replying, setReplying] = useState(() => drafts.has(replyKey));
  const [opened, setOpened] = useState(false);
  const replyButton = useRef<HTMLButtonElement>(null);
  const resolved = root.resolved === true;
  const closeReply = () => {
    drafts.delete(replyKey);
    setReplying(false);
  };
  const comment = (c: CommentBody, reply: boolean) => (
    <CommentItem
      key={c.id}
      send={send}
      drafts={drafts}
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
      {resolved && root.resolved_at !== undefined && (
        <p className="text-sm text-fg-muted">
          {root.resolved_by === undefined
            ? t("comments.resolved_at", {
                at: formatRelativeTime(
                  i18n.language,
                  new Date(root.resolved_at),
                ),
              })
            : t("comments.resolved_by", {
                name: root.resolved_by.display_name,
                at: formatRelativeTime(
                  i18n.language,
                  new Date(root.resolved_at),
                ),
              })}
        </p>
      )}
      {writable &&
        (replying ? (
          <ReplyForm
            send={send}
            rootId={root.id}
            draft={draftOf(drafts, replyKey)}
            autoFocus={opened}
            onCancel={() => {
              closeReply();
              requestAnimationFrame(() => replyButton.current?.focus());
            }}
            // The Reply button comes back as the form goes, which takes the
            // focus that the form had, rather than from where the user put
            // it meanwhile.
            onDone={(focused) => {
              closeReply();
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
              onClick={() => {
                draftOf(drafts, replyKey);
                setOpened(true);
                setReplying(true);
              }}
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
 * which opens with what draft holds of it, and whose comment's ID is the
 * one that the form keeps for its body while it is open (keptId), so that
 * Post reply pressed again once no answer came, with the text as it was,
 * sends the same batch, which the server answers as it did the first rather
 * than post the reply twice.
 */
function ReplyForm({
  send,
  rootId,
  draft,
  autoFocus,
  onCancel,
  onDone,
}: {
  send: Send;
  rootId: string;
  /** What the form holds, unsent, which the page's document keeps while it is open (draftsOf). */
  draft: Draft;
  /** Whether the text takes the focus as the form shows, as once Reply's press opened it, and not once it is made again. */
  autoFocus: boolean;
  onCancel: () => void;
  onDone: (focused: boolean) => void;
}) {
  const { t } = useTranslation();
  const [initial] = useState(() => draft.body);
  return (
    <CommentForm
      label={t("comments.reply_label")}
      submit={t("comments.reply_post")}
      initial={initial}
      autoFocus={autoFocus}
      onEdit={(value) => {
        if (value?.doc !== undefined) {
          draft.body = value.doc;
        }
      }}
      onCancel={onCancel}
      onSend={async (body) => {
        await send([
          {
            type: "comment.create",
            payload: { id: keptId(draft, body), parent_id: rootId, body },
          },
        ]);
      }}
      onDone={onDone}
    />
  );
}

/** A comment: who wrote it and when, its body, and the user's own buttons of it, or, deleted, that it is, as the server lists a thread's first comment that is deleted while a reply of it is not. */
function CommentItem({
  send,
  drafts,
  comment: c,
  reply,
  own,
  busy,
  idOf,
  onDelete,
}: {
  send: Send;
  /** The drafts of the forms of the threads, which an edit's opens with (draftsOf). */
  drafts: Map<string, Draft>;
  comment: CommentBody;
  reply: boolean;
  /** Whether the comment is the user's own, which they edit and delete. */
  own: boolean;
  busy: boolean;
  idOf: (what: string, commentId: string) => string;
  onDelete: () => void;
}) {
  const { t, i18n } = useTranslation();
  // The edit's form is open while its draft is (draftsOf), as it was made
  // last, and shows while the comment is the user's own to change (own),
  // though it was not as the form was made again, as while the project was
  // archived; Edit's press alone gives its field the focus (opened). A
  // draft is only ever of the user's own comment, as the page's document
  // goes with the account.
  const editKey = draftKey("edit", c.id);
  const [editing, setEditing] = useState(() => drafts.has(editKey));
  const [opened, setOpened] = useState(false);
  const editButton = useRef<HTMLButtonElement>(null);
  const bylineId = idOf("byline", c.id);
  const closeEdit = () => {
    drafts.delete(editKey);
    setEditing(false);
  };
  if (c.deleted || c.body === undefined) {
    return (
      <div>
        <p id={bylineId} className="text-fg-muted italic">
          {reply ? t("comments.reply_deleted") : t("comments.deleted")}
        </p>
      </div>
    );
  }
  const posted = new Date(c.created_at);
  const name = c.author?.display_name ?? t("comments.no_author");
  return (
    <div className="space-y-2">
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
      {/* An edit open is the user's own comment's, while they may still
      change it, as no longer once the project is archived meanwhile. */}
      {editing && own ? (
        <EditForm
          comment={c}
          body={c.body}
          draft={draftOf(drafts, editKey)}
          autoFocus={opened}
          send={send}
          onCancel={() => {
            closeEdit();
            requestAnimationFrame(() => editButton.current?.focus());
          }}
          // Edit comes back as the form goes, which takes the focus that the
          // form had, rather than from where the user put it meanwhile.
          onDone={(focused) => {
            closeEdit();
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
                onClick={() => {
                  draftOf(drafts, editKey).rev = c.rev;
                  setOpened(true);
                  setEditing(true);
                }}
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
  return conflictsOf(error).find((c) => canonical(c.id) === canonical(id))
    ?.current_rev;
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
 * expect the revision that it was refused for again. It opens with what
 * draft holds of it, its text and the revision that it expects, as it was
 * made last (draftsOf).
 */
function EditForm({
  comment: c,
  body,
  draft,
  autoFocus,
  send,
  onCancel,
  onDone,
}: {
  comment: CommentBody;
  body: RichTextDoc;
  /** What the form holds, unsent, which the page's document keeps while it is open (draftsOf). */
  draft: Draft;
  /** Whether the text takes the focus as the form shows, as once Edit's press opened it, and not once it is made again. */
  autoFocus: boolean;
  send: Send;
  onCancel: () => void;
  onDone: (focused: boolean) => void;
}) {
  const { t } = useTranslation();
  const [initial] = useState(() => draft.body ?? body);
  const [rev, setRev] = useState(() => draft.rev ?? c.rev);
  return (
    <CommentForm
      label={t("comments.edit_label")}
      submit={t("comments.save")}
      initial={initial}
      autoFocus={autoFocus}
      onEdit={(value) => {
        if (value?.doc !== undefined) {
          draft.body = value.doc;
        }
      }}
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
            draft.rev = now;
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
 * whose text is read-only while its batch is under way, as the canvas's
 * editor of a text is, so that nothing is typed after what was sent, which
 * the form, emptied or closed once its comment is posted, would lose, and
 * its button, which says that it is unavailable while its batch is
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
  /** The body that the form starts with, as an edit's, or what it held as it was made last (draftsOf), but not once its comment is posted. */
  initial?: RichTextDoc;
  /** Whether the text takes the focus as the form shows, as a reply's and an edit's, which their button opened. */
  autoFocus?: boolean;
  /** Called as the text changes, with what it is then. */
  onEdit?: (value: RichTextValue | undefined) => void;
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
        editable={!send.isPending}
        defaultValue={posted === 0 ? initial : undefined}
        error={problem ?? refusal?.fields.body ?? refusal?.message}
        onChange={(next) => {
          setValue(next);
          setProblem(undefined);
          onEdit?.(next);
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
