// Play mode's saved play states (PRD FR-PLAY-05, plan note N80): the
// project's named states of the flow, which every member who reads the
// project sees and loads as a starting point, and which an owner, an admin,
// or an editor saves, as the flow is now, renames, replaces with the flow as
// it is now, and deletes, each a command of the page's history, which its
// undo takes back. Loading one starts play at it, with what loading could
// not keep of it, which the panel says (PlayPanel).

import { ApiError, type PlayStateSummary } from "@storycreep/shared/api";
import { formatDateTime } from "@storycreep/shared/i18n";
import { newId } from "@storycreep/shared/ids";
import { useMutation, useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import {
  Activity,
  type ChangeEvent,
  type FormEvent,
  memo,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { type Refusal, refusalOf } from "../auth/refusal";
import { collator } from "../content/textNames";
import type { ProjectDocument } from "../document/store";
import { useServerReachable } from "../offline/pending";
import { payloadField, useExpected } from "../project/dialogs";
import { focusIsNowhere, showInPage } from "../shell/focus";
import { Button } from "../ui/Button";
import { TextField } from "../ui/Field";
import { Alert, Status } from "../ui/Page";
import { escaped, keepSheetOpen } from "./keys";
import { playStatesQuery, readPlayState } from "./queries";
import { followsFlow, type PlaySession } from "./session";
import { reportCount } from "./steps";

// The IDs of the form that saves a play state, whose field of its name the
// palette's Save play state goes to, and of the heading of the saved play
// states, which the palette's Saved play states goes to: the board's page
// shows one play panel at most.
const saveFormId = "play-saved-form";
const savedHeadingId = "play-saved-title";

/**
 * Gives the focus to the field of the name of a play state to save, of the
 * play panel of the page doc, as the palette's Save play state does, or to
 * the heading of the saved play states, as its Saved play states does, if
 * the panel shows them.
 */
export function goToSaved(doc: Document, what: "save" | "list"): void {
  if (what === "list") {
    showInPage(doc, savedHeadingId);
    return;
  }
  const field = doc.getElementById(saveFormId)?.querySelector("input");
  field?.scrollIntoView?.({ block: "center" });
  field?.focus({ preventScroll: true });
}

/** The most characters that a play state's name may have, as the server takes it. */
const maxName = 100;

// The status of the last load of each session that left the flow on another
// board, with that board and the turn that it was, which the section of that
// board's page, which the board's page follows the flow to (followsFlow),
// says once it shows, as long as the turn is still the session's, rather
// than nothing, as the section that loaded it goes with its own page; the
// section of another board's page, as the one that loaded it shown anew
// while the page waits to follow the flow (holding), says nothing of it.
const followedLoads = new WeakMap<
  PlaySession,
  { readonly board: string; readonly turn: number; readonly status: string }
>();

// The forms of the section, whose name's field shows a refusal of the
// server that names it.
type NameForm = "save" | "rename";

// What the section said last, of what it did or why it could not: of a
// refusal of a form's press, the form, under whose name's field a refusal of
// the name is shown, rather than under another's, and the error that it
// was, after which a rename expects the revision that the list has, once it
// was refused for one that changed elsewhere (useExpected).
type Said =
  | { readonly status: string }
  | {
      readonly refusal: Refusal | undefined;
      readonly alert?: string;
      readonly form?: NameForm;
      readonly error?: unknown;
    };

// The section, which renders only as what it shows changes, rather than as
// every turn of the session renders the panel, since it reads the session's
// state only as a press asks for it.
export const SavedStates = memo(function SavedStates({
  doc,
  session,
  boardId,
  editable,
}: {
  doc: ProjectDocument;
  session: PlaySession;
  /** The board whose page shows the section. */
  boardId: string;
  /** Whether the user may save, rename, replace, and delete them: an owner's, an admin's, or an editor's role, of a project that is not archived. */
  editable: boolean;
}) {
  const { t, i18n } = useTranslation();
  const list = useQuery(playStatesQuery(doc));
  // The saved states are the server's, no part of the project's copy, nor
  // of the changes that the device keeps (PRD FR-OFF-03): while the page
  // cannot reach the server, as offline, the section says that they need a
  // connection, in place of a list and controls that would fail, even of a
  // list read before, which is hidden, and its form's Save is disabled, the
  // forms keeping the names typed, the save's and a rename's, as through a
  // try that got no answer, which ends once the next is answered
  // (DocumentState.holding).
  const online = useServerReachable(doc);
  const userId = useQuery(sessionQuery).data?.user_id;
  const headingRef = useRef<HTMLHeadingElement>(null);
  // The prefix of the IDs of the states' lines of when each last changed,
  // which describe the buttons of each, as two of a name are told apart.
  const changedIds = useId();
  const [said, setSaid] = useState<Said>();
  const [busy, setBusy] = useState(false);
  const [renaming, setRenaming] = useState<string>();
  // The status of a load that the page of another board made, which left
  // the flow on this one, is said once the section shows, into its status
  // as it is there, as a screen reader hears a change of it (followedLoads).
  useEffect(() => {
    const followed = followedLoads.get(session);
    if (followed === undefined || followed.board !== boardId) {
      return;
    }
    followedLoads.delete(session);
    const s = session.state;
    if (s.status === "playing" && s.turn === followed.turn) {
      setSaid({ status: followed.status });
    }
  }, [session, boardId]);
  // headingAfter gives the section's heading the focus once a press of the
  // section whose control goes with what it did is done, as a deletion, a
  // rename saved, or Try again, from where the press left it: pressed, the
  // control that had it as the press began, or nowhere, as once the browser
  // took it from that control as the press disabled it, but not from where
  // the user has put it while the press was under way. What another
  // session's change takes away, as the button of a state that it deleted,
  // leaves the focus where the browser leaves it (plan note N80).
  const headingAfter = (pressed: Element | null) => {
    if (document.activeElement === pressed || focusIsNowhere(document)) {
      headingRef.current?.focus();
    }
  };
  // What a press runs, as a request of the page's, which the app's query
  // client records once it fails, as it records a mutation's, but for a
  // refusal of the server's, which the section says (createQueryClient), so
  // that a fault of the app's, which the section says the error console
  // has, is there.
  const press = useMutation({
    mutationFn: (run: () => Promise<string | Said | undefined>) => run(),
  });
  // act runs what a press asked for, one at a time, and says what it did,
  // or why it could not, the server's refusal among them, which is form's,
  // the form whose press it was, if any, or nothing for undefined, as of a
  // press while play does not play; run is given the control that had the
  // focus as it was pressed (headingAfter). It resolves to whether it did
  // what was asked, so that a form keeps what was typed in it where it did
  // not.
  const act = async (
    run: (pressed: Element | null) => Promise<string | Said | undefined>,
    form?: NameForm,
  ): Promise<boolean> => {
    if (busy) {
      return false;
    }
    const pressed = document.activeElement;
    setBusy(true);
    try {
      const done = await press.mutateAsync(() => run(pressed));
      setSaid(typeof done === "string" ? { status: done } : done);
      return typeof done === "string";
    } catch (error) {
      // A refusal of the server's, an answer below 500 with its error
      // envelope, rather than a fault of the server's or the instance's, or
      // no answer, which a read again would meet as well (refusalOf), is
      // of a list that may be behind the server, which the list read again
      // shows as it is: a state that changed elsewhere, whose revision a
      // rename pressed again then expects (useExpected), or that another
      // session deleted while the page did not hear of it, as a load of it
      // is refused for.
      if (
        error instanceof ApiError &&
        error.status < 500 &&
        error.error !== undefined
      ) {
        await list.refetch();
      }
      setSaid({
        refusal: refusalOf(error, t, payloadField("name")),
        form,
        error,
      });
      return false;
    } finally {
      setBusy(false);
    }
  };
  // The newest first of a name, by when each last changed, compared as
  // times rather than as the API writes them, whose fractions of a second
  // are as long as they need be.
  const states = [...(list.data ?? [])].sort(
    (a, b) =>
      collator.compare(a.name, b.name) ||
      Date.parse(b.updated_at) - Date.parse(a.updated_at),
  );
  // A rename whose state the list no longer shows, as once another session
  // deleted it, ends with its field, rather than open again, taking the
  // focus from wherever the user has put it, as the state comes back, as by
  // the undo of its deletion; one whose list could not be read again stays,
  // as the list shows the states that it read last.
  if (renaming !== undefined && !states.some((s) => s.id === renaming)) {
    setRenaming(undefined);
  }
  const load = (s: PlayStateSummary) =>
    act(async () => {
      const pressed = session.state;
      const flow = pressed.status === "playing" ? pressed.flow : undefined;
      const body = await readPlayState(doc, s.id);
      // Play went on while the state was read, as by a choice, a step back,
      // a value set by hand, which is no turn, Reset, Restart, or a start
      // from the canvas, each of which leaves another flow, which loading it
      // now would undo: the later press wins, as a start that another
      // replaced does nothing more once its reads are answered
      // (PlaySession.start).
      const answered = session.state;
      if (answered.status === "playing" && answered.flow !== flow) {
        return t("play.saved.load_overtaken", { name: body.name });
      }
      try {
        if (
          !session.load({
            name: body.name,
            state: body.state,
            names: body.names,
          })
        ) {
          return undefined;
        }
      } catch (error) {
        // A state that the runtime does not load, as one of a newer
        // stateVersion, which the server takes none of, of which it throws
        // its StateError, told by its name, since the page reads the runtime
        // only once play starts (PlaySession.load); any other error is a
        // fault of the app's, which the section says as one, and the error
        // console records (press).
        if (!(error instanceof Error && error.name === "StateError")) {
          throw error;
        }
        return {
          refusal: undefined,
          alert: t("play.saved.unloadable", {
            name: body.name,
            reason: error instanceof Error ? error.message : String(error),
          }),
        };
      }
      // What loading could not keep, which the panel lists, as many as the
      // status says, which the section of the page of the board that the
      // load left the flow on says, where it is another's.
      const now = session.state;
      const report =
        now.status === "playing"
          ? now.flow.history[0]?.loaded?.report
          : undefined;
      const status = t("play.saved.loaded_status", {
        name: body.name,
        count: report === undefined ? 0 : reportCount(report),
      });
      if (
        now.status === "playing" &&
        followsFlow(doc, now.flow.board, boardId)
      ) {
        followedLoads.set(session, {
          board: now.flow.board,
          turn: now.turn,
          status,
        });
      }
      return status;
    });
  const replace = (s: PlayStateSummary) =>
    act(async () => {
      const now = session.saving();
      if (now === undefined) {
        return undefined;
      }
      await doc.send([
        {
          type: "play_state.set_state",
          payload: { id: s.id, state: now.state, names: now.names },
          expected_rev: { [s.id]: s.rev },
        },
      ]);
      return t("play.saved.replaced", { name: s.name });
    });
  const remove = (s: PlayStateSummary) =>
    act(async (pressed) => {
      await doc.send([
        {
          type: "play_state.delete",
          payload: { id: s.id },
          expected_rev: { [s.id]: s.rev },
        },
      ]);
      // The row goes with the state, and with it the button pressed.
      headingAfter(pressed);
      return t("play.saved.deleted", { name: s.name });
    });
  const refusal = said !== undefined && "refusal" in said ? said : undefined;
  // What the server refused of form's name, which its field shows.
  const refusalOfForm = (form: NameForm) =>
    refusal?.form === form ? refusal.refusal : undefined;
  // The error that the server refused the last rename with, if it was.
  const renameError = refusal?.form === "rename" ? refusal.error : undefined;
  return (
    // No Escape in the section closes the sheet that the panel is, which
    // would stop play and lose its history, as one pressed once more than
    // there was to empty or to cancel would, once the focus is on the
    // heading after a rename, a deletion, or a rename cancelled: in a field
    // it empties the name or cancels the rename alone (escaped), as the
    // inspector's do (plan note N79, keepSheetOpen).
    <section
      aria-labelledby={savedHeadingId}
      className="space-y-2"
      onKeyDown={keepSheetOpen}
    >
      <h3
        id={savedHeadingId}
        ref={headingRef}
        tabIndex={-1}
        className="font-semibold"
      >
        {t("play.saved.title")}
      </h3>
      {editable && (
        <SaveForm
          disabled={busy || !online}
          onSave={(name) =>
            act(async () => {
              const now = session.saving();
              if (now === undefined) {
                return undefined;
              }
              await doc.send([
                {
                  type: "play_state.create",
                  payload: {
                    id: newId("play_state"),
                    name,
                    state: now.state,
                    names: now.names,
                  },
                },
              ]);
              return t("play.saved.saved", { name });
            }, "save")
          }
          refusal={refusalOfForm("save")}
        />
      )}
      <Status
        message={
          said !== undefined && "status" in said ? said.status : undefined
        }
      />
      {/* A refusal's message leaves out what it places at the field of its
          form's name, which says it there (refusalOf), and says the rest,
          as of a state too large refused with the name. */}
      <Alert message={refusal?.alert ?? refusal?.refusal?.message} />
      {(list.isPending || !online) && (
        // Offline, the list waits for the browser to be online again, which
        // the device's copy of the project does not hold (PRD FR-OFF-02).
        <p className="text-fg-muted">
          {online ? t("play.saved.reading") : t("play.saved.offline")}
        </p>
      )}
      {/* A list read is hidden meanwhile, rather than taken away, so that a
          rename under way keeps the name typed, and the revision that its
          field opened with, as through a try that got no answer. */}
      {!list.isPending && (
        <Activity mode={online ? "visible" : "hidden"}>
          {/* A list that could not be read says why, and one that could
              not be read again says it beside the states that it read last,
              as the list of the tokens does, rather than take them away,
              with a rename under way, for a read that the user did not ask
              for, as once another session saved one. */}
          {list.isError && (
            <div className="space-y-2">
              <Alert
                message={[
                  t("play.saved.read_failed"),
                  refusalOf(list.error, t).message,
                ]
                  .filter((m) => m !== undefined)
                  .join(" ")}
              />
              <Button
                onClick={() => {
                  const pressed = document.activeElement;
                  void list.refetch().then((read) => {
                    // The button goes once the list is read.
                    if (!read.isError) {
                      headingAfter(pressed);
                    }
                  });
                }}
              >
                {t("play.saved.read_again")}
              </Button>
            </div>
          )}
          {list.data === undefined ? null : states.length === 0 ? (
            <p className="text-fg-muted">
              {editable ? t("play.saved.none_editable") : t("play.saved.none")}
            </p>
          ) : (
            <ul className="space-y-2">
              {states.map((s) => (
                <li
                  key={s.id}
                  className="space-y-2 rounded-md border border-border p-2"
                >
                  {renaming === s.id ? (
                    <RenameForm
                      state={s}
                      disabled={busy}
                      refusal={refusalOfForm("rename")}
                      error={renameError}
                      onCancel={() => {
                        // A refusal of the rename goes with its field, as the
                        // focus in it, or on its Cancel, does.
                        setSaid((was) =>
                          was !== undefined &&
                          "form" in was &&
                          was.form === "rename"
                            ? undefined
                            : was,
                        );
                        setRenaming(undefined);
                        headingRef.current?.focus();
                      }}
                      onRename={(name, rev) =>
                        act(async (pressed) => {
                          await doc.send([
                            {
                              type: "play_state.rename",
                              payload: { id: s.id, name },
                              expected_rev: { [s.id]: rev },
                            },
                          ]);
                          setRenaming(undefined);
                          headingAfter(pressed);
                          return t("play.saved.renamed", {
                            from: s.name,
                            name,
                          });
                        }, "rename")
                      }
                    />
                  ) : (
                    <>
                      <p className="font-medium break-words">{s.name}</p>
                      {/* Which describes each of its buttons, which the
                          name alone names, as it names those of another
                          state of the name. */}
                      <p
                        id={`${changedIds}-${s.id}`}
                        className="text-sm text-fg-muted"
                      >
                        {changedWhen(t, i18n.language, s, userId)}
                      </p>
                      <div className="flex flex-wrap gap-2">
                        <Button
                          disabled={busy}
                          aria-label={t("play.saved.load_named", {
                            name: s.name,
                          })}
                          aria-describedby={`${changedIds}-${s.id}`}
                          onClick={() => void load(s)}
                        >
                          {t("play.saved.load")}
                        </Button>
                        {editable && (
                          <>
                            <Button
                              disabled={busy}
                              aria-label={t("play.saved.replace_named", {
                                name: s.name,
                              })}
                              aria-describedby={`${changedIds}-${s.id}`}
                              onClick={() => void replace(s)}
                            >
                              {t("play.saved.replace")}
                            </Button>
                            <Button
                              disabled={busy}
                              aria-label={t("play.saved.rename_named", {
                                name: s.name,
                              })}
                              aria-describedby={`${changedIds}-${s.id}`}
                              onClick={() => {
                                setSaid(undefined);
                                setRenaming(s.id);
                              }}
                            >
                              {t("play.saved.rename")}
                            </Button>
                            <Button
                              disabled={busy}
                              aria-label={t("play.saved.delete_named", {
                                name: s.name,
                              })}
                              aria-describedby={`${changedIds}-${s.id}`}
                              onClick={() => void remove(s)}
                            >
                              {t("play.saved.delete")}
                            </Button>
                          </>
                        )}
                      </div>
                    </>
                  )}
                </li>
              ))}
            </ul>
          )}
        </Activity>
      )}
    </section>
  );
});

// changedWhen returns when the play state s last changed, its name or its
// state, as the list says it, and whether by the user whose ID is userId: a
// rename changes it as a save and a replace do, which the server does not
// tell apart.
function changedWhen(
  t: TFunction,
  language: string,
  s: PlayStateSummary,
  userId: string | undefined,
): string {
  const at = formatDateTime(language, new Date(s.updated_at));
  return s.updated_by !== undefined && s.updated_by === userId
    ? t("play.saved.changed_by_you", { at })
    : t("play.saved.changed_at", { at });
}

// nameProblem returns why name, a play state's name as typed, is none that
// the server takes, once the white space around it is gone, or undefined.
function nameProblem(t: TFunction, name: string): string | undefined {
  if (name === "") {
    return t("play.saved.name_empty");
  }
  if ([...name].length > maxName) {
    return t("play.saved.name_long", { max: maxName });
  }
  return undefined;
}

// refusedName returns what refusal, the server's of a form's last press,
// says of the name that the press sent, sent, while the form's field holds
// it, name, as typed, and nothing once the name is changed, as the form's
// own problem of it goes as it is changed.
function refusedName(
  refusal: Refusal | undefined,
  name: string,
  sent: string | undefined,
): string | undefined {
  return name.trim() === sent ? refusal?.fields.name : undefined;
}

// useNameField holds the name of a play state that a form's field asks for,
// as typed, from initial, with why it is none that the server takes once the
// form was pressed (nameProblem), and the name that the form's last press
// sent, of which refusal, the server's of that press, says what it does
// under the field while the field holds it (refusedName), as the save's and
// the rename's forms ask for one alike.
function useNameField(initial: string, refusal: Refusal | undefined) {
  const { t } = useTranslation();
  const [name, setName] = useState(initial);
  const [problem, setProblem] = useState<string>();
  const [sent, setSent] = useState<string>();
  return {
    /** The props of the field's TextField, but for its label and hint. */
    field: {
      error: problem ?? refusedName(refusal, name, sent),
      value: name,
      maxLength: maxName * 2,
      onChange: (event: ChangeEvent<HTMLInputElement>) => {
        setName(event.target.value);
        setProblem(undefined);
      },
    },
    /**
     * Returns the name to send, without the white space around it, or
     * undefined, of one that the server takes none of, which the field then
     * says why of.
     */
    toSend: (): string | undefined => {
      const trimmed = name.trim();
      const p = nameProblem(t, trimmed);
      setProblem(p);
      if (p !== undefined) {
        return undefined;
      }
      setSent(trimmed);
      return trimmed;
    },
    /** Empties the field, and what it said of the name. */
    clear: () => {
      setName("");
      setProblem(undefined);
    },
  };
}

// SaveForm asks for the name of the play state to save, which the flow's
// state is saved as.
function SaveForm({
  disabled,
  onSave,
  refusal,
}: {
  disabled: boolean;
  /** Saves the flow's state under the name, and resolves to whether it did. */
  onSave: (name: string) => Promise<boolean>;
  /** Why the server refused the last save, whose name field it may name. */
  refusal: Refusal | undefined;
}) {
  const { t } = useTranslation();
  const name = useNameField("", refusal);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const sent = name.toSend();
    // The name stays where nothing was saved, as once the server refused it,
    // which says why under the field, or play stopped meanwhile.
    if (sent !== undefined && (await onSave(sent))) {
      name.clear();
    }
  };
  return (
    // Escape empties the name, as the inspector's filter is emptied.
    <form
      id={saveFormId}
      onSubmit={(e) => void submit(e)}
      onKeyDown={escaped(name.clear)}
      className="space-y-2"
      noValidate
    >
      <TextField
        label={t("play.saved.name")}
        hint={t("play.saved.name_hint")}
        {...name.field}
      />
      <Button type="submit" variant="primary" disabled={disabled}>
        {t("play.saved.save")}
      </Button>
    </form>
  );
}

// RenameForm asks for the new name of the play state state, and renames it
// expecting the revision that it had once the form opened, so that a rename
// elsewhere meanwhile, which the list shows as it reads it again, is refused
// rather than written over unasked (PRD §7.10), or, once a rename was
// refused for it, which the section says, the one that the list has then
// (useExpected).
function RenameForm({
  state,
  disabled,
  refusal,
  error,
  onCancel,
  onRename,
}: {
  state: PlayStateSummary;
  /** Whether a press of the section is under way, while which the rename stays, as a dialog stays open while its request is, so that its refusal is said under its field. */
  disabled: boolean;
  /** Why the server refused the last rename, whose name field it may name. */
  refusal: Refusal | undefined;
  /** The error that the server refused the last rename with, if it did. */
  error: unknown;
  onCancel: () => void;
  /** Renames the state to name, expecting its revision rev, and resolves to whether it did. */
  onRename: (name: string, rev: number) => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const name = useNameField(state.name, refusal);
  const rev = useExpected(state.rev, error);
  const cancel = () => {
    if (!disabled) {
      onCancel();
    }
  };
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const sent = name.toSend();
    if (sent !== undefined) {
      await onRename(sent, rev);
    }
  };
  return (
    // Escape cancels the rename, as its button does, but for while a press
    // is under way (disabled).
    <form
      onSubmit={(e) => void submit(e)}
      onKeyDown={escaped(cancel)}
      className="space-y-2"
      noValidate
    >
      <TextField
        label={t("play.saved.new_name", { name: state.name })}
        autoFocus
        {...name.field}
      />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={disabled}>
          {t("play.saved.rename_save")}
        </Button>
        <Button disabled={disabled} onClick={cancel}>
          {t("play.saved.rename_cancel")}
        </Button>
      </div>
    </form>
  );
}
