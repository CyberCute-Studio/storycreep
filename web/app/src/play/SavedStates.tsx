// Play mode's saved play states (PRD FR-PLAY-05, plan note N80): the
// project's named states of the flow, which every member who reads the
// project sees and loads as a starting point, and which an owner, an admin,
// or an editor saves, as the flow is now, renames, replaces with the flow as
// it is now, and deletes, each a command of the page's history, which its
// undo takes back. Loading one starts play at it, with what loading could
// not keep of it, which the panel says (PlayPanel).

import type { PlayStateSummary } from "@storycreep/shared/api";
import { formatDateTime } from "@storycreep/shared/i18n";
import { newId } from "@storycreep/shared/ids";
import { useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { type FormEvent, type KeyboardEvent, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { sessionQuery } from "../api";
import { type Refusal, refusalOf } from "../auth/refusal";
import { collator } from "../content/textNames";
import type { ProjectDocument } from "../document/store";
import { payloadField } from "../project/dialogs";
import { inputMethodTook } from "../richtext/suggest";
import { showInPage } from "../shell/focus";
import { Button } from "../ui/Button";
import { TextField } from "../ui/Field";
import { Alert, Status } from "../ui/Page";
import { playStatesQuery, readPlayState } from "./queries";
import type { PlaySession } from "./session";
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

// keepsSheet handles Escape in a field of the section, which does what onEscape
// says rather than close the sheet that the panel is, which would stop play
// and lose its history, as the inspector's fields do (plan note N79), but
// for an Escape that cancels what an input method was composing, which is
// the input method's, and does nothing else (inputMethodTook).
function keepsSheet(onEscape: () => void) {
  return (event: KeyboardEvent<HTMLElement>) => {
    if (event.key !== "Escape") {
      return;
    }
    event.stopPropagation();
    if (!inputMethodTook(event.nativeEvent)) {
      onEscape();
    }
  };
}

/** The most characters that a play state's name may have, as the server takes it. */
const maxName = 100;

// The forms of the section, whose name's field shows a refusal of the
// server that names it.
type NameForm = "save" | "rename";

// What the section said last, of what it did or why it could not: of a
// refusal of a form's press, the form, under whose name's field a refusal of
// the name is shown, rather than under another's.
type Said =
  | { readonly status: string }
  | {
      readonly refusal: Refusal | undefined;
      readonly alert?: string;
      readonly form?: NameForm;
    };

export function SavedStates({
  doc,
  session,
  editable,
}: {
  doc: ProjectDocument;
  session: PlaySession;
  /** Whether the user may save, rename, replace, and delete them: an owner's, an admin's, or an editor's role, of a project that is not archived. */
  editable: boolean;
}) {
  const { t, i18n } = useTranslation();
  const list = useQuery(playStatesQuery(doc));
  const userId = useQuery(sessionQuery).data?.user_id;
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [said, setSaid] = useState<Said>();
  const [busy, setBusy] = useState(false);
  const [renaming, setRenaming] = useState<string>();
  // act runs what a press asked for, one at a time, and says what it did,
  // or why it could not, the server's refusal among them, which is form's,
  // the form whose press it was, if any, or nothing for undefined, as of a
  // press while play does not play. It resolves to whether it did what was
  // asked, so that a form keeps what was typed in it where it did not.
  const act = async (
    run: () => Promise<string | Said | undefined>,
    form?: NameForm,
  ): Promise<boolean> => {
    if (busy) {
      return false;
    }
    setBusy(true);
    try {
      const done = await run();
      setSaid(typeof done === "string" ? { status: done } : done);
      return typeof done === "string";
    } catch (error) {
      setSaid({ refusal: refusalOf(error, t, payloadField("name")), form });
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
  const load = (s: PlayStateSummary) =>
    act(async () => {
      const pressed = session.state;
      const flow = pressed.status === "playing" ? pressed.flow : undefined;
      const body = await readPlayState(doc.projectId, s.id);
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
            id: body.id,
            name: body.name,
            state: body.state,
            names: body.names,
          })
        ) {
          return undefined;
        }
      } catch (error) {
        // A state that the runtime does not load, as one of a newer
        // stateVersion, which the server takes none of.
        return {
          refusal: undefined,
          alert: t("play.saved.unloadable", {
            name: body.name,
            reason: error instanceof Error ? error.message : String(error),
          }),
        };
      }
      // What loading could not keep, which the panel lists, as many as the
      // status says.
      const now = session.state;
      const report =
        now.status === "playing"
          ? now.flow.history[0]?.loaded?.report
          : undefined;
      return t("play.saved.loaded_status", {
        name: body.name,
        count: report === undefined ? 0 : reportCount(report),
      });
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
    act(async () => {
      await doc.send([
        {
          type: "play_state.delete",
          payload: { id: s.id },
          expected_rev: { [s.id]: s.rev },
        },
      ]);
      // The row goes with the state, and with it the button pressed.
      headingRef.current?.focus();
      return t("play.saved.deleted", { name: s.name });
    });
  const refusal = said !== undefined && "refusal" in said ? said : undefined;
  // What the server refused of form's name, which its field shows.
  const refusalOfForm = (form: NameForm) =>
    refusal?.form === form ? refusal.refusal : undefined;
  return (
    <section aria-labelledby={savedHeadingId} className="space-y-2">
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
          disabled={busy}
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
      {list.isPending ? (
        <p className="text-fg-muted">{t("play.saved.reading")}</p>
      ) : list.isError ? (
        <div className="space-y-2">
          <Alert
            message={[
              t("play.saved.read_failed"),
              refusalOf(list.error, t).message,
            ]
              .filter((m) => m !== undefined)
              .join(" ")}
          />
          <Button onClick={() => void list.refetch()}>
            {t("play.saved.read_again")}
          </Button>
        </div>
      ) : states.length === 0 ? (
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
                  onCancel={() => {
                    // A refusal of the rename goes with its field.
                    setSaid((was) =>
                      was !== undefined &&
                      "form" in was &&
                      was.form === "rename"
                        ? undefined
                        : was,
                    );
                    setRenaming(undefined);
                  }}
                  onRename={(name) =>
                    act(async () => {
                      await doc.send([
                        {
                          type: "play_state.rename",
                          payload: { id: s.id, name },
                          expected_rev: { [s.id]: s.rev },
                        },
                      ]);
                      setRenaming(undefined);
                      headingRef.current?.focus();
                      return t("play.saved.renamed", { from: s.name, name });
                    }, "rename")
                  }
                />
              ) : (
                <>
                  <p className="font-medium break-words">{s.name}</p>
                  <p className="text-sm text-fg-muted">
                    {changedWhen(t, i18n.language, s, userId)}
                  </p>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      disabled={busy}
                      aria-label={t("play.saved.load_named", { name: s.name })}
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
                          onClick={() => void replace(s)}
                        >
                          {t("play.saved.replace")}
                        </Button>
                        <Button
                          disabled={busy}
                          aria-label={t("play.saved.rename_named", {
                            name: s.name,
                          })}
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
    </section>
  );
}

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
  const [name, setName] = useState("");
  const [problem, setProblem] = useState<string>();
  const [sent, setSent] = useState<string>();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    const p = nameProblem(t, trimmed);
    setProblem(p);
    if (p === undefined) {
      setSent(trimmed);
      // The name stays where nothing was saved, as once the server refused
      // it, which says why under the field, or play stopped meanwhile.
      if (await onSave(trimmed)) {
        setName("");
      }
    }
  };
  return (
    // Escape empties the name, as the inspector's filter is emptied.
    <form
      id={saveFormId}
      onSubmit={(e) => void submit(e)}
      onKeyDown={keepsSheet(() => {
        setName("");
        setProblem(undefined);
      })}
      className="space-y-2"
      noValidate
    >
      <TextField
        label={t("play.saved.name")}
        hint={t("play.saved.name_hint")}
        error={problem ?? refusedName(refusal, name, sent)}
        value={name}
        maxLength={maxName * 2}
        onChange={(e) => {
          setName(e.target.value);
          setProblem(undefined);
        }}
      />
      <Button type="submit" variant="primary" disabled={disabled}>
        {t("play.saved.save")}
      </Button>
    </form>
  );
}

// RenameForm asks for the new name of the play state state.
function RenameForm({
  state,
  disabled,
  refusal,
  onCancel,
  onRename,
}: {
  state: PlayStateSummary;
  disabled: boolean;
  /** Why the server refused the last rename, whose name field it may name. */
  refusal: Refusal | undefined;
  onCancel: () => void;
  onRename: (name: string) => Promise<boolean>;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(state.name);
  const [problem, setProblem] = useState<string>();
  const [sent, setSent] = useState<string>();
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    const p = nameProblem(t, trimmed);
    setProblem(p);
    if (p === undefined) {
      setSent(trimmed);
      await onRename(trimmed);
    }
  };
  return (
    // Escape cancels the rename, as its button does.
    <form
      onSubmit={(e) => void submit(e)}
      onKeyDown={keepsSheet(onCancel)}
      className="space-y-2"
      noValidate
    >
      <TextField
        label={t("play.saved.new_name", { name: state.name })}
        error={problem ?? refusedName(refusal, name, sent)}
        value={name}
        maxLength={maxName * 2}
        autoFocus
        onChange={(e) => {
          setName(e.target.value);
          setProblem(undefined);
        }}
      />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={disabled}>
          {t("play.saved.rename_save")}
        </Button>
        <Button onClick={onCancel}>{t("play.saved.rename_cancel")}</Button>
      </div>
    </form>
  );
}
