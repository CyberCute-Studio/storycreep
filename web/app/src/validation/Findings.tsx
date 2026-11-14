// The findings of a project's validation (PRD FR-DEP-03, plan note N77), as
// its page lists them: each with what is wrong, as the server says it, and
// where, as a link that opens the editor of what it is about (AC3), and,
// of a warning or an info, a button that mutes it with a reason, which the
// page then lists apart, with its reason, who muted it, and when, and
// whose mute is changed or taken off there (AC4).

import type { Finding } from "@storycreep/shared/api";
import { formatDateTime } from "@storycreep/shared/i18n";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { Dialog } from "radix-ui";
import { type FormEvent, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useShallow } from "zustand/react/shallow";
import { refusalOf } from "../auth/refusal";
import { nodeTitleText } from "../content/textNames";
import { useDocumentState, useProjectDocument } from "../document/react";
import { payloadField } from "../project/dialogs";
import { useCommands } from "../project/queries";
import { Button } from "../ui/Button";
import {
  closesBy,
  DialogFrame,
  FormButtons,
  focusBackTo,
  type Opener,
} from "../ui/Dialog";
import { TextAreaField, useFocusRefusal } from "../ui/Field";
import { Alert, textLink } from "../ui/Page";
import { type MutedCode, mutedCode } from "./queries";

/** The names of the variables and the components that findings are about, by their IDs, as the project's document has them. */
export interface FindingNames {
  variable: (id: string) => string | undefined;
  component: (id: string) => string | undefined;
}

/**
 * Returns the keys of the variables and the names of the components that
 * findings are about, as the project's document has them: the document
 * reads its variables, or its components, once a finding is about one, so
 * that a finding is named by it rather than as a variable's or a
 * component's; a read that failed, which the document records, leaves it
 * named so. The list follows the names of what it lists alone.
 */
export function useFindingNames(
  projectId: string,
  findings: readonly Finding[],
): FindingNames {
  const doc = useProjectDocument(projectId);
  const of = (type: Finding["entity_type"]) => [
    ...new Set(
      findings.filter((f) => f.entity_type === type).map((f) => f.entity_id),
    ),
  ];
  const variables = of("variable");
  const components = of("component");
  const keys = useDocumentState(
    doc,
    useShallow((state) =>
      variables.map((id) => state.view.variables.get(id)?.key),
    ),
  );
  const names = useDocumentState(
    doc,
    useShallow((state) =>
      components.map((id) => state.view.components.get(id)?.name),
    ),
  );
  const readsVariables = variables.length > 0;
  const readsComponents = components.length > 0;
  useEffect(() => {
    if (readsVariables && !doc.holds("variables")) {
      void doc.load("variables").catch(() => undefined);
    }
    if (readsComponents && !doc.holds("components")) {
      void doc.load("components").catch(() => undefined);
    }
  }, [doc, readsVariables, readsComponents]);
  return {
    variable: (id) => keys[variables.indexOf(id)],
    component: (id) => names[components.indexOf(id)],
  };
}

/** Returns where finding is, of what it is about, and of which field: a node of its board, a choice or an output of one, a board, a variable, a component's description, or the project's entry. */
export function whereText(
  t: TFunction,
  finding: Finding,
  names: FindingNames,
): string {
  const field = finding.field ?? "none";
  const node = nodeTitleText(t, finding.node_title ?? "");
  const board = finding.board_name ?? t("boards.unknown_board");
  switch (finding.entity_type) {
    case "node":
      return t("validation.where.node", { field, node, board });
    case "connection":
      return t("validation.where.connection", { field, node, board });
    case "branch_output":
      return t("validation.where.branch_output", { field, node, board });
    case "board":
      return t("validation.where.board", { field, board });
    case "variable": {
      const key =
        names.variable(finding.entity_id) ?? t("validation.a_variable");
      return finding.board_id === undefined
        ? t("validation.where.global_variable", { key })
        : t("validation.where.board_variable", { key, board });
    }
    case "component":
      return t("validation.where.component", {
        component: names.component(finding.entity_id) ?? t("usage.a_component"),
      });
    case "project":
      return t("validation.where.project");
  }
}

/**
 * Where finding is, as a link that opens the editor of what it is about
 * (AC3): a node, or the node of a branch's output, selected on its board's
 * canvas, in the inspector of a board that may be changed (plan item 61); a
 * connection selected so too, in its own inspector, or its node on a board
 * that may not be changed; a board's canvas; and the panel of a variable or
 * a component. The project's entry, which no page edits yet, is named
 * without a link.
 */
function FindingWhere({
  projectId,
  finding,
  names,
}: {
  projectId: string;
  finding: Finding;
  names: FindingNames;
}) {
  const { t } = useTranslation();
  const text = whereText(t, finding, names);
  const { board_id: board, node_id: node } = finding;
  switch (finding.entity_type) {
    case "node":
    case "branch_output":
    case "connection":
      if (board === undefined || node === undefined) {
        break;
      }
      return (
        <Link
          to="/projects/$projectId/boards/$boardId"
          params={{ projectId, boardId: board }}
          search={{
            node,
            ...(finding.entity_type === "connection"
              ? { connection: finding.entity_id }
              : {}),
          }}
          className={textLink}
        >
          {text}
        </Link>
      );
    case "board":
      return (
        <Link
          to="/projects/$projectId/boards/$boardId"
          params={{ projectId, boardId: finding.entity_id }}
          className={textLink}
        >
          {text}
        </Link>
      );
    case "variable":
      return (
        <Link
          to="/projects/$projectId/variables"
          params={{ projectId }}
          search={{ variable: finding.entity_id }}
          className={textLink}
        >
          {text}
        </Link>
      );
    case "component":
      return (
        <Link
          to="/projects/$projectId/components"
          params={{ projectId }}
          search={{ component: finding.entity_id }}
          className={textLink}
        >
          {text}
        </Link>
      );
  }
  return <span>{text}</span>;
}

/** What a finding's mute is changed to: its owner, its code, and its reason as it is, if it has one. */
export interface Muting {
  finding: Finding;
  code: MutedCode;
  opener: HTMLElement;
}

/**
 * A finding of the report: what is wrong, where, by a link to its editor,
 * its code, and, of a muted one, its mute; and its buttons: Mute of one not
 * muted that a mute may silence, or Change reason and Unmute of one muted,
 * where the user may change the project.
 */
export function FindingItem({
  projectId,
  finding,
  names,
  editable,
  userId,
  onMute,
  onUnmute,
}: {
  projectId: string;
  finding: Finding;
  names: FindingNames;
  /** Whether the user may mute and unmute it. */
  editable: boolean;
  /** The signed-in user's ID, which a mute that they made names. */
  userId: string | undefined;
  onMute: (muting: Muting) => void;
  onUnmute: (finding: Finding, code: MutedCode) => void;
}) {
  const { t, i18n } = useTranslation();
  const code = mutedCode(finding);
  const mute = finding.mute;
  return (
    <li className="space-y-1 border-t border-border py-3">
      <p className="break-words">{finding.message}</p>
      <p className="break-words text-fg-muted">
        <FindingWhere projectId={projectId} finding={finding} names={names} />
        {" · "}
        <code>{finding.code}</code>
      </p>
      {mute !== undefined && (
        <div className="space-y-1">
          <p className="break-words whitespace-pre-line">
            {t("validation.mute_reason", { reason: mute.reason })}
          </p>
          <p className="text-fg-muted">
            {mute.muted_by !== undefined && mute.muted_by === userId
              ? t("validation.muted_by_you", {
                  at: formatDateTime(i18n.language, new Date(mute.muted_at)),
                })
              : t("validation.muted_at", {
                  at: formatDateTime(i18n.language, new Date(mute.muted_at)),
                })}
          </p>
        </div>
      )}
      {editable && code !== undefined && (
        <div className="flex flex-wrap gap-2 pt-1">
          {mute === undefined ? (
            <MuteButton
              label={t("validation.mute")}
              finding={finding}
              code={code}
              names={names}
              onMute={onMute}
            />
          ) : (
            <>
              <MuteButton
                label={t("validation.change_reason")}
                finding={finding}
                code={code}
                names={names}
                onMute={onMute}
              />
              <Button
                aria-label={t("validation.button_of", {
                  label: t("validation.unmute"),
                  where: whereText(t, finding, names),
                })}
                onClick={() => onUnmute(finding, code)}
              >
                {t("validation.unmute")}
              </Button>
            </>
          )}
        </div>
      )}
    </li>
  );
}

/** The button that opens the dialog of a finding's mute, named by where the finding is, as a list of many shows many. */
function MuteButton({
  label,
  finding,
  code,
  names,
  onMute,
}: {
  label: string;
  finding: Finding;
  code: MutedCode;
  names: FindingNames;
  onMute: (muting: Muting) => void;
}) {
  const { t } = useTranslation();
  return (
    <Button
      aria-haspopup="dialog"
      aria-label={t("validation.button_of", {
        label,
        where: whereText(t, finding, names),
      })}
      onClick={(event) =>
        onMute({ finding, code, opener: event.currentTarget })
      }
    >
      {label}
    </Button>
  );
}

const reasonField = payloadField("reason");

/** The most characters that a mute's reason may have (FR-DEP-03). */
export const reasonLength = 500;

/**
 * Returns reason as a mute's reason is sent: without white space around it,
 * with its line breaks written as line feeds, as the server takes it.
 */
export function reasonOf(reason: string): string {
  return reason.replace(/\r\n?/g, "\n").trim();
}

/**
 * The dialog that mutes a finding, a warning's or an info's code on its
 * entity, with a reason of 1 to 500 characters, which it asks for, or that
 * changes the reason of its mute (validation.mute), and closes once the
 * server has applied it, giving the focus to onMuted's choice, as the
 * button that opened it goes with the finding to the other list.
 */
export function MuteDialog({
  projectId,
  muting,
  names,
  onClose,
  onMuted,
}: {
  projectId: string;
  muting: Muting;
  names: FindingNames;
  onClose: () => void;
  /** Tells that the mute applied, before the dialog closes, and returns where the focus goes then. */
  onMuted: () => Opener;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const commands = useCommands(projectId);
  const refusal = refusalOf(commands.error, t, reasonField);
  useFocusRefusal(form, commands.error);
  const focusTo = useRef<Opener>(muting.opener);
  const { finding, code } = muting;
  const where = whereText(t, finding, names);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const reason = reasonOf(
      String(new FormData(event.currentTarget).get("reason")),
    );
    commands.mutate(
      [
        {
          type: "validation.mute",
          payload: { entity_id: finding.entity_id, code, reason },
        },
      ],
      {
        onSuccess: () => {
          focusTo.current = onMuted();
          onClose();
        },
      },
    );
  };
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, commands.isPending)}>
      <DialogFrame
        title={
          finding.mute === undefined
            ? t("validation.mute_title", { where })
            : t("validation.change_title", { where })
        }
        description={finding.message}
        onCloseAutoFocus={focusBackTo(() => {
          const to = focusTo.current;
          return typeof to === "function" ? to() : (to ?? null);
        })}
      >
        <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
          <Alert message={refusal?.message} />
          <TextAreaField
            label={t("validation.reason")}
            hint={t("validation.reason_hint", { max: reasonLength })}
            name="reason"
            required
            maxLength={reasonLength}
            defaultValue={finding.mute?.reason ?? ""}
            error={refusal?.fields.reason}
          />
          <FormButtons
            cancel={t("validation.cancel")}
            pending={commands.isPending}
            submit={
              finding.mute === undefined
                ? t("validation.mute_submit")
                : t("validation.change_submit")
            }
          />
        </form>
      </DialogFrame>
    </Dialog.Root>
  );
}
