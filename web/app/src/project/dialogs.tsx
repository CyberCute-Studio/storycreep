// The dialogs that change the tree of a project's boards (PRD FR-PROJ-02):
// creating a board or a folder, renaming one, moving one, which the tree's
// dragging does too, but for a keyboard, a screen reader, or a single tap
// (WCAG 2.5.7), and deleting one to the trash, a board after it lists the
// jumpers on other boards that its deletion would leave broken (AC2); and
// those of the folders of the project's variables and components, which
// create, rename, move, and delete them alike (FR-VAR-01, FR-COMP-02). Each
// sends a batch of commands (useCommands), expecting the revision of what it
// changes that it opened with (useExpected), and gives the focus back to the
// control that opened it, or to the page's content once that control has
// gone, as with its item.

import {
  ApiError,
  type Board,
  type Folder,
  type Node,
} from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { useQuery } from "@tanstack/react-query";
import { AlertDialog, Dialog } from "radix-ui";
import { type FormEvent, type ReactNode, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { refusalOf } from "../auth/refusal";
import {
  ConfirmButtons,
  ConfirmFrame,
  closesBy,
  DialogFrame,
  FormButtons,
  focusBackTo,
  type Opener,
} from "../ui/Dialog";
import { SelectField, TextField, useFocusRefusal } from "../ui/Field";
import { Alert, RefusalAlert } from "../ui/Page";
import {
  type Command,
  conflictOf,
  referrersQuery,
  referrersShown,
  useCommands,
} from "./queries";
import {
  childrenOf,
  type Drop,
  type FolderNode,
  folderChoices,
  type Item,
  nameOf,
  type Placement,
  placementOf,
  revOf,
  type Tree,
  type TreeNode,
} from "./tree";

/** The field of a command's payload that a refusal's location names, as body.commands[0].payload.name. */
export function payloadField(...fields: string[]) {
  return (location: string): string | undefined => {
    const field = location.replace(/^body\.commands\[\d+\]\.payload\./, "");
    return fields.includes(field) ? field : undefined;
  };
}

/** The value of the choice of a folder that stands for the top of the tree. */
const top = "";

/** The value of FolderSelect's choice of the top of the tree. */
export const folderTop = top;

/** The type of the command that moves an item of a tree into a folder of its kind, by the item's ID. */
function itemMoveType(
  id: string,
): "board.move" | "variable.move" | "component.move" {
  return id.startsWith("var_")
    ? "variable.move"
    : id.startsWith("cmp_")
      ? "component.move"
      : "board.move";
}

/**
 * Returns the commands that move node to placement, expecting rev of it, the
 * revision that the tree shows of it unless given.
 */
export function moveCommands(
  node: TreeNode<Item>,
  placement: Placement,
  rev = revOf(node),
): Command[] {
  const order = placement.order === undefined ? {} : { order: placement.order };
  return node.kind === "item"
    ? [
        {
          type: itemMoveType(node.id),
          payload: {
            id: node.id,
            ...(placement.parentId === undefined
              ? {}
              : { folder_id: placement.parentId }),
            ...order,
          },
          expected_rev: { [node.id]: rev },
        },
      ]
    : [
        {
          type: "folder.move",
          payload: {
            id: node.id,
            ...(placement.parentId === undefined
              ? {}
              : { parent_id: placement.parentId }),
            ...order,
          },
          expected_rev: { [node.id]: rev },
        },
      ];
}

/**
 * Returns the revision that a dialog's next attempt expects of what it
 * changes, of which the tree shows rev now: the one that it had once the
 * dialog opened, so that a change made elsewhere while it is open, which
 * the project's document shows as it commits, is not changed over unasked
 * (PRD §7.10, FR-API-03 AC3), or, once an attempt was refused for such a
 * change (error), which the dialog says, and the tree shows, the one that
 * it has then. The document moves it past the page's own batches that
 * applied since, as a drag that was under way once the dialog opened, which
 * are no change made elsewhere (ProjectDocument.send).
 */
export function useExpected(rev: number, error: unknown): number {
  const [held, setHeld] = useState({ rev, error });
  if (error !== held.error) {
    const next = { rev: conflictOf(error) ? rev : held.rev, error };
    setHeld(next);
    return next.rev;
  }
  return held.rev;
}

interface FrameProps {
  title: string;
  description?: string;
  opener: Opener;
  /** Whether the dialog's request is under way, while which it stays open. */
  pending: boolean;
  onClose: () => void;
  children: ReactNode;
}

/** A dialog of the tree, which stays open while its request is under way. */
function TreeDialog({
  title,
  description,
  opener,
  pending,
  onClose,
  children,
}: FrameProps) {
  return (
    <Dialog.Root open onOpenChange={closesBy(onClose, pending)}>
      <DialogFrame
        title={title}
        description={description}
        onCloseAutoFocus={focusBackTo(opener)}
      >
        {children}
      </DialogFrame>
    </Dialog.Root>
  );
}

/** The choice of a folder of the tree, or its top, whose value is the folder's ID, or folderTop. */
export function FolderSelect({
  tree,
  node,
  label,
  value,
  onChange,
  error,
}: {
  tree: Tree<Item>;
  /** What moves, which cannot go into itself, nor into a folder within it. */
  node?: TreeNode<Item>;
  label: string;
  value: string;
  onChange?: (value: string) => void;
  error?: string;
}) {
  const { t } = useTranslation();
  return (
    <SelectField
      label={label}
      name="folder"
      value={value}
      onChange={(event) => onChange?.(event.currentTarget.value)}
      error={error}
    >
      <option value={top}>{t("boards.top")}</option>
      {folderChoices(tree, node).map((choice) => (
        <option key={choice.id} value={choice.id}>
          {choice.path.join(" / ")}
        </option>
      ))}
    </SelectField>
  );
}

/**
 * The choice of a folder of the tree, or its top, as a form's field named
 * folder, which starts at initial, the folder's ID or undefined for the
 * top, and keeps what the user chooses: a form shown anew, with a key of
 * its own, starts it anew.
 */
export function FolderField({
  tree,
  label,
  initial,
  error,
}: {
  tree: Tree<Item>;
  label: string;
  initial: string | undefined;
  error?: string;
}) {
  const [value, setValue] = useState(initial ?? top);
  return (
    <FolderSelect
      tree={tree}
      label={label}
      value={value}
      onChange={setValue}
      error={error}
    />
  );
}

const nameField = payloadField("name");

/**
 * The dialog that creates a board, or a folder of folderKind's, of boards
 * unless it says otherwise, in parentId's folder or at the top.
 */
export function CreateDialog({
  projectId,
  tree,
  kind,
  folderKind = "board",
  parentId,
  opener,
  onClose,
  onCreated,
}: {
  projectId: string;
  tree: Tree<Item>;
  kind: "board" | "folder";
  folderKind?: Folder["kind"];
  parentId: string | undefined;
  opener: Opener;
  onClose: () => void;
  onCreated: (name: string) => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const [chosen, setFolder] = useState(parentId ?? top);
  // The folder chosen, as the tree has it now: one that has gone from it
  // since it was chosen, as once the tree was read again, shows as the
  // first choice, the top, as a select shows a value that none of its
  // options has, and the dialog creates what it creates there, as it shows,
  // as MoveDialog does.
  const folder = folderChoices(tree).some((c) => c.id === chosen)
    ? chosen
    : top;
  // The ID of what the dialog creates, the same for each attempt, so that
  // an attempt sent again once no answer came to it creates nothing more.
  const [id] = useState(() => newId(kind));
  const commands = useCommands(projectId);
  const refusal = refusalOf(commands.error, t, nameField);
  useFocusRefusal(form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const name = String(new FormData(event.currentTarget).get("name"));
    const command: Command =
      kind === "board"
        ? {
            type: "board.create",
            payload: {
              id,
              name,
              ...(folder === top ? {} : { folder_id: folder }),
            },
          }
        : {
            type: "folder.create",
            payload: {
              id,
              kind: folderKind,
              name,
              ...(folder === top ? {} : { parent_id: folder }),
            },
          };
    commands.mutate([command], {
      onSuccess: () => {
        onCreated(name);
        onClose();
      },
    });
  };
  return (
    <TreeDialog
      title={
        kind === "board"
          ? t("boards.create_board_title")
          : t("boards.create_folder_title")
      }
      opener={opener}
      pending={commands.isPending}
      onClose={onClose}
    >
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={
            kind === "board" ? t("boards.board_name") : t("boards.folder_name")
          }
          name="name"
          autoComplete="off"
          required
          maxLength={100}
          error={refusal?.fields.name}
        />
        <FolderSelect
          tree={tree}
          label={t("boards.in_folder")}
          value={folder}
          onChange={setFolder}
        />
        <FormButtons
          cancel={t("boards.cancel")}
          pending={commands.isPending}
          submit={
            kind === "board"
              ? t("boards.create_board")
              : t("boards.create_folder")
          }
        />
      </form>
    </TreeDialog>
  );
}

/** The dialog that renames a board or a folder, of any kind. */
export function RenameDialog({
  projectId,
  node,
  opener,
  onClose,
}: {
  projectId: string;
  node: TreeNode | FolderNode<Item>;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const form = useRef<HTMLFormElement>(null);
  const commands = useCommands(projectId);
  const rev = useExpected(revOf(node), commands.error);
  const refusal = refusalOf(commands.error, t, nameField);
  useFocusRefusal(form, commands.error);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const name = String(new FormData(event.currentTarget).get("name"));
    const command: Command =
      node.kind === "item"
        ? {
            type: "board.rename",
            payload: { id: node.id, name },
            expected_rev: { [node.id]: rev },
          }
        : {
            type: "folder.rename",
            payload: { id: node.id, name },
            expected_rev: { [node.id]: rev },
          };
    commands.mutate([command], { onSuccess: onClose });
  };
  return (
    <TreeDialog
      title={t("boards.rename_title", { name: nameOf(node) })}
      opener={opener}
      pending={commands.isPending}
      onClose={onClose}
    >
      <form ref={form} className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusal?.message} />
        <TextField
          label={t("boards.new_name")}
          name="name"
          autoComplete="off"
          required
          maxLength={100}
          defaultValue={nameOf(node)}
          error={refusal?.fields.name}
        />
        <FormButtons
          cancel={t("boards.cancel")}
          pending={commands.isPending}
          submit={t("boards.rename_submit")}
        />
      </form>
    </TreeDialog>
  );
}

/** The value of the choice of a place that stands for after the folder's last child. */
const atEnd = "end";
/** The value of the choice of a place that stands for before the folder's first child. */
const atStart = "start";

/**
 * The dialog that moves a board or a folder, as dragging it does: into a
 * folder, or to the top, at its start, at its end, or after one of its
 * children.
 */
export function MoveDialog({
  projectId,
  tree,
  node,
  opener,
  onClose,
}: {
  projectId: string;
  tree: Tree<Item>;
  node: TreeNode<Item>;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [folder, setFolder] = useState(node.parentId ?? top);
  const [place, setPlace] = useState(atEnd);
  const commands = useCommands(projectId);
  const rev = useExpected(revOf(node), commands.error);
  // Where the choices say, as the tree has them now: a folder, or a child
  // to go after, that has gone from them since it was chosen, as once the
  // tree was read again, shows as the first choice, as a select shows a
  // value that none of its options has, and the dialog moves the child to
  // what it shows, rather than after a child that went elsewhere, or
  // nowhere.
  const parentId = folderChoices(tree, node).some((c) => c.id === folder)
    ? folder
    : undefined;
  const siblings = childrenOf(tree, parentId).filter((c) => c.id !== node.id);
  const first = siblings[0];
  const after = siblings.find((c) => c.id === place);
  const drop: Drop =
    after !== undefined
      ? { position: "after", targetId: after.id }
      : place === atStart && first !== undefined
        ? { position: "before", targetId: first.id }
        : { position: "inside", folderId: parentId };
  const shown = after?.id ?? (drop.position === "before" ? atStart : atEnd);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const placement = placementOf(tree, node, drop);
    if (placement !== undefined) {
      commands.mutate(moveCommands(node, placement, rev), {
        onSuccess: onClose,
      });
    }
  };
  return (
    <TreeDialog
      title={t("boards.move_title", { name: nameOf(node) })}
      opener={opener}
      pending={commands.isPending}
      onClose={onClose}
    >
      <form className="space-y-4" onSubmit={submit} noValidate>
        <Alert message={refusalOf(commands.error, t)?.message} />
        <FolderSelect
          tree={tree}
          node={node}
          label={t("boards.move_to")}
          value={parentId ?? top}
          onChange={(value) => {
            setFolder(value);
            setPlace(atEnd);
          }}
        />
        <SelectField
          label={t("boards.place")}
          name="place"
          value={shown}
          onChange={(event) => setPlace(event.currentTarget.value)}
        >
          <option value={atEnd}>{t("boards.place_end")}</option>
          {siblings.length > 0 && (
            <option value={atStart}>{t("boards.place_start")}</option>
          )}
          {siblings.map((sibling) => (
            <option key={sibling.id} value={sibling.id}>
              {t("boards.place_after", { name: nameOf(sibling) })}
            </option>
          ))}
        </SelectField>
        <FormButtons
          cancel={t("boards.cancel")}
          pending={commands.isPending}
          submit={t("boards.move_submit")}
        />
      </form>
    </TreeDialog>
  );
}

/** The confirmation that deletes a folder, which holds nothing that is not in the trash. */
export function DeleteFolderDialog({
  projectId,
  folder,
  opener,
  onClose,
}: {
  projectId: string;
  folder: Folder;
  opener: Opener;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const commands = useCommands(projectId);
  const rev = useExpected(folder.rev, commands.error);
  return (
    <Confirm
      title={t("boards.delete_folder_title", { name: folder.name })}
      description={t("boards.delete_folder_body")}
      confirm={t("boards.delete_folder")}
      opener={opener}
      pending={commands.isPending}
      onClose={onClose}
      onConfirm={() =>
        commands.mutate(
          [
            {
              type: "folder.delete",
              payload: { id: folder.id },
              expected_rev: { [folder.id]: rev },
            },
          ],
          { onSuccess: onClose },
        )
      }
    >
      <Alert message={refusalOf(commands.error, t)?.message} />
    </Confirm>
  );
}

/** A jumper that targets a node of a board, as a refusal of the board's deletion lists it. */
interface Jumper {
  id: string;
  board_id: string;
  title: string;
}

/**
 * The confirmation that deletes a board to the trash, which first lists the
 * jumpers on the project's other boards that target its nodes, which its
 * deletion would leave broken, for validation to report (PRD FR-PROJ-02
 * AC2), and then deletes it, confirmed, if it listed any. A board that such
 * a jumper targets once the list was read is refused, with the jumpers,
 * which the dialog then lists, to confirm.
 */
export function DeleteBoardDialog({
  projectId,
  board,
  boards,
  opener,
  onClose,
  onDeleted,
}: {
  projectId: string;
  board: Board;
  /** The project's boards, whose names the list of jumpers shows. */
  boards: readonly Board[];
  opener: Opener;
  onClose: () => void;
  onDeleted?: () => void;
}) {
  const { t } = useTranslation();
  const referrers = useQuery(referrersQuery(projectId, board.id));
  const commands = useCommands(projectId);
  const rev = useExpected(board.rev, commands.error);
  const refused = referencedBy(commands.error);
  const jumpers: Jumper[] | undefined =
    refused?.jumpers ??
    referrers.data?.items.map((node: Node) => ({
      id: node.id,
      board_id: node.board_id,
      title: node.title,
    }));
  const count = refused?.count ?? jumpers?.length ?? 0;
  const more =
    refused === undefined
      ? (referrers.data?.more ?? false)
      : count > (jumpers?.length ?? 0);
  const confirmed = count > 0;
  const boardName = (id: string) =>
    boards.find((b) => b.id === id)?.name ?? t("boards.unknown_board");
  return (
    <Confirm
      title={t("boards.delete_board_title", { name: board.name })}
      description={t("boards.delete_board_body")}
      confirm={
        confirmed
          ? t("boards.delete_board_anyway", { count })
          : t("boards.delete_board")
      }
      opener={opener}
      pending={commands.isPending}
      // The list is read before the deletion, so that it is confirmed only
      // once what it breaks is shown, while Cancel closes the dialog all the
      // same, as while a read that is slow, or that waits for the browser's
      // connection, is under way.
      ready={!referrers.isPending}
      onClose={onClose}
      onConfirm={() =>
        commands.mutate(
          [
            {
              type: "board.delete",
              payload: {
                id: board.id,
                ...(confirmed ? { confirm: true } : {}),
              },
              expected_rev: { [board.id]: rev },
            },
          ],
          {
            // What deleted the board hears of it before the dialog closes,
            // as the board's page, which goes to the project's then.
            onSuccess: () => {
              onDeleted?.();
              onClose();
            },
          },
        )
      }
    >
      {referrers.isError && (
        <Alert message={refusalOf(referrers.error, t)?.message} />
      )}
      {jumpers !== undefined && jumpers.length > 0 && (
        <div className="space-y-2">
          <p className="font-medium">
            {/* The first page of the list says that there are more, but
                not how many, which a refusal alone counts. */}
            {refused === undefined && more
              ? t("boards.jumpers_intro_more", { count })
              : t("boards.jumpers_intro", { count })}
          </p>
          <ul className="max-h-48 list-disc space-y-1 overflow-y-auto ps-6">
            {jumpers.map((jumper) => (
              <li key={jumper.id} className="break-words">
                {t("boards.jumper", {
                  title:
                    jumper.title === ""
                      ? t("boards.untitled_node")
                      : jumper.title,
                  board: boardName(jumper.board_id),
                })}
              </li>
            ))}
          </ul>
          {more && (
            <p className="text-fg-muted">
              {t("boards.jumpers_more", { shown: referrersShown })}
            </p>
          )}
        </div>
      )}
      {refused === undefined && (
        <RefusalAlert refusal={refusalOf(commands.error, t)} />
      )}
    </Confirm>
  );
}

/** The jumpers of a refusal of a board's deletion that was not confirmed (board_referenced). */
function referencedBy(
  error: unknown,
): { jumpers: Jumper[]; count: number } | undefined {
  if (
    !(error instanceof ApiError) ||
    error.error?.code !== "board_referenced"
  ) {
    return undefined;
  }
  const details = error.error.details ?? {};
  const listed = Array.isArray(details.jumpers) ? details.jumpers : [];
  const jumpers = listed.filter(
    (j): j is Jumper =>
      typeof j?.id === "string" &&
      typeof j?.board_id === "string" &&
      typeof j?.title === "string",
  );
  const count =
    typeof details.jumper_count === "number"
      ? details.jumper_count
      : jumpers.length;
  return { jumpers, count };
}

/**
 * A confirmation of a destructive change, which stays open while it is
 * under way, and is confirmed once it is ready (ConfirmButtons).
 */
function Confirm({
  title,
  description,
  confirm,
  opener,
  pending,
  ready,
  onClose,
  onConfirm,
  children,
}: {
  title: string;
  description: string;
  confirm: string;
  opener: Opener;
  pending: boolean;
  ready?: boolean;
  onClose: () => void;
  onConfirm: () => void;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <AlertDialog.Root open onOpenChange={closesBy(onClose, pending)}>
      <ConfirmFrame
        title={title}
        description={description}
        onCloseAutoFocus={focusBackTo(opener)}
      >
        {children}
        <ConfirmButtons
          cancel={t("boards.cancel")}
          confirm={confirm}
          pending={pending}
          ready={ready}
          onConfirm={onConfirm}
        />
      </ConfirmFrame>
    </AlertDialog.Root>
  );
}
