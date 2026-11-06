// The list of a project's variables or components in the tree of their
// folders (PRD FR-VAR-01, FR-COMP-02): its folders, which open and close,
// each with a menu of what the user's role may do to it, and the variables
// or the components that they hold, each a button that opens its panel. A
// project has a thousand variables or more (FR-VAR-01 AC3), so an item is a
// button and its text alone: what may be done to one is in its panel, which
// moves it to another folder too. While a filter is on, the list shows the
// items that match it alone, each with the path of its folder.

import type { Folder } from "@storycreep/shared/api";
import { type ReactNode, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  CreateDialog,
  DeleteFolderDialog,
  MoveDialog,
  RenameDialog,
} from "../project/dialogs";
import {
  type FolderNode,
  folderChoices,
  type Item,
  type ItemNode,
  type Tree,
  type TreeNode,
} from "../project/tree";
import { Button } from "../ui/Button";
import type { Opener } from "../ui/Dialog";
import { Menu, MenuItem, MenuSeparator } from "../ui/Menu";
import { Status } from "../ui/Page";

/** A dialog of a folder that the list shows, with the control that opened it. */
type Open<T extends Item> =
  | { dialog: "create"; parentId: string | undefined; opener: Opener }
  | {
      dialog: "rename" | "move" | "delete";
      node: FolderNode<T>;
      opener: Opener;
    };

export interface ContentTreeProps<T extends Item> {
  projectId: string;
  tree: Tree<T>;
  folderKind: Folder["kind"];
  editable: boolean;
  /** The ID of the list's heading. */
  labelledBy: string;
  /**
   * The items that match the filter, in the tree's order, which the list
   * shows alone, or undefined while no filter is on.
   */
  matches: readonly ItemNode<T>[] | undefined;
  /** The item whose panel is open, which the list marks. */
  selected: string | undefined;
  onSelect: (id: string) => void;
  /** What an item's button shows. */
  renderItem: (item: T) => ReactNode;
  /** The label of a folder's menu's item that creates an item in it. */
  newItemHere: string;
  /** Creates an item in the folder parentId, as its menu asks, from opener. */
  onCreateIn: (parentId: string, opener: Opener) => void;
  /**
   * What the list shows before its items, as the page's buttons, given what
   * opens the dialog that creates a folder at the top.
   */
  header: (newFolder: (opener: Opener) => void) => ReactNode;
}

/** Returns the attribute that marks the button of the item of id, by which the page finds it again (itemButton). */
function itemMark(id: string) {
  return { "data-item-id": id };
}

/** Returns the button of the item of id, as the list shows it now, or null. */
export function itemButton(id: string): HTMLElement | null {
  return document.querySelector<HTMLElement>(
    `[data-item-id="${CSS.escape(id)}"]`,
  );
}

/** The classes of an item's button, of the one whose panel is open too, which a bar beside it marks, besides its colour. */
export function itemClasses(selected: boolean): string {
  return `flex min-h-11 w-full min-w-0 flex-col items-start justify-center rounded-md border-s-4 px-2 py-1 text-start ${
    selected
      ? "border-accent bg-surface"
      : "border-transparent hover:bg-surface-hover"
  }`;
}

export function ContentTree<T extends Item>(props: ContentTreeProps<T>) {
  const { t } = useTranslation();
  const { projectId, tree, folderKind, matches, labelledBy } = props;
  const [closed, setClosed] = useState<ReadonlySet<string>>(new Set());
  const [open, setOpen] = useState<Open<T>>();
  const [created, setCreated] = useState<string>();
  const paths = useMemo(
    () =>
      new Map(folderChoices(tree).map((choice) => [choice.id, choice.path])),
    [tree],
  );
  const toggle = (id: string) =>
    setClosed((before) => {
      const after = new Set(before);
      if (!after.delete(id)) {
        after.add(id);
      }
      return after;
    });
  const close = () => setOpen(undefined);
  // The folder that a dialog changes, as the tree has it now, or, once it
  // has gone from the tree, as it was (dialogs.tsx).
  const folder =
    open === undefined || open.dialog === "create"
      ? undefined
      : ((tree.byId.get(open.node.id) as FolderNode<T> | undefined) ??
        open.node);
  return (
    <>
      {props.header((opener) =>
        setOpen({ dialog: "create", parentId: undefined, opener }),
      )}
      <Status
        message={
          created === undefined
            ? undefined
            : t("boards.created", { name: created })
        }
      />
      {matches !== undefined ? (
        <ul className="space-y-1" aria-labelledby={labelledBy}>
          {matches.map((node) => (
            <li key={node.id}>
              <button
                type="button"
                {...itemMark(node.id)}
                aria-current={props.selected === node.id ? "true" : undefined}
                className={itemClasses(props.selected === node.id)}
                onClick={() => props.onSelect(node.id)}
              >
                {props.renderItem(node.item)}
                {node.parentId !== undefined && (
                  <span className="text-sm break-words text-fg-muted">
                    {t("content.in_folder", {
                      path: (paths.get(node.parentId) ?? []).join(" / "),
                    })}
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <TreeList
          {...props}
          nodes={tree.top}
          labelledBy={labelledBy}
          closed={closed}
          onToggle={toggle}
          onOpen={setOpen}
        />
      )}
      {open?.dialog === "create" && (
        <CreateDialog
          projectId={projectId}
          tree={tree}
          kind="folder"
          folderKind={folderKind}
          parentId={open.parentId}
          opener={open.opener}
          onClose={close}
          onCreated={setCreated}
        />
      )}
      {open?.dialog === "rename" && folder !== undefined && (
        <RenameDialog
          projectId={projectId}
          node={folder}
          opener={open.opener}
          onClose={close}
        />
      )}
      {open?.dialog === "move" && folder !== undefined && (
        <MoveDialog
          projectId={projectId}
          tree={tree}
          node={folder}
          opener={open.opener}
          onClose={close}
        />
      )}
      {open?.dialog === "delete" && folder !== undefined && (
        <DeleteFolderDialog
          projectId={projectId}
          folder={folder.folder}
          opener={open.opener}
          onClose={close}
        />
      )}
    </>
  );
}

interface ListProps<T extends Item>
  extends Omit<ContentTreeProps<T>, "labelledBy"> {
  nodes: TreeNode<T>[];
  /** The ID of the list's heading, of the top's list alone. */
  labelledBy?: string;
  closed: ReadonlySet<string>;
  onToggle: (id: string) => void;
  onOpen: (open: Open<T>) => void;
}

/** A list of the tree's children, each folder with its own under it. */
function TreeList<T extends Item>({
  nodes,
  labelledBy,
  ...props
}: ListProps<T>) {
  return (
    <ul className="space-y-1" aria-labelledby={labelledBy}>
      {nodes.map((node) =>
        node.kind === "folder" ? (
          <FolderItem key={node.id} node={node} {...props} />
        ) : (
          <li key={node.id}>
            <button
              type="button"
              {...itemMark(node.id)}
              aria-current={props.selected === node.id ? "true" : undefined}
              className={itemClasses(props.selected === node.id)}
              onClick={() => props.onSelect(node.id)}
            >
              {props.renderItem(node.item)}
            </button>
          </li>
        ),
      )}
    </ul>
  );
}

function FolderItem<T extends Item>({
  node,
  ...props
}: Omit<ListProps<T>, "nodes" | "labelledBy"> & { node: FolderNode<T> }) {
  const { t } = useTranslation();
  const { editable, closed, onToggle, onOpen } = props;
  const isOpen = !closed.has(node.id);
  const name = node.folder.name;
  // The folder's menu, where the list shows it once a dialog has closed: in
  // another place, once the dialog moved it, whose button is another.
  const opener = () =>
    document.querySelector<HTMLElement>(
      `[data-folder-actions="${CSS.escape(node.id)}"]`,
    );
  return (
    <li>
      <div className="flex min-h-11 items-center gap-1">
        <button
          type="button"
          className="flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-start font-medium hover:bg-surface-hover"
          aria-expanded={isOpen}
          onClick={() => onToggle(node.id)}
        >
          <span aria-hidden="true">{isOpen ? "▾" : "▸"}</span>
          <span className="min-w-0 break-words">{name}</span>
        </button>
        {editable && (
          <Menu
            trigger={
              <Button
                data-folder-actions={node.id}
                aria-label={t("boards.actions", { name })}
              >
                <span aria-hidden="true">⋯</span>
              </Button>
            }
          >
            <MenuItem onSelect={() => props.onCreateIn(node.id, opener)}>
              {props.newItemHere}
            </MenuItem>
            <MenuItem
              onSelect={() =>
                onOpen({ dialog: "create", parentId: node.id, opener })
              }
            >
              {t("boards.new_folder_here")}
            </MenuItem>
            <MenuSeparator />
            <MenuItem
              onSelect={() => onOpen({ dialog: "rename", node, opener })}
            >
              {t("boards.rename")}
            </MenuItem>
            <MenuItem onSelect={() => onOpen({ dialog: "move", node, opener })}>
              {t("boards.move")}
            </MenuItem>
            <MenuSeparator />
            <MenuItem
              danger
              onSelect={() => onOpen({ dialog: "delete", node, opener })}
            >
              {t("boards.delete")}
            </MenuItem>
          </Menu>
        )}
      </div>
      {isOpen && node.children.length > 0 && (
        <div className="ms-6 border-s border-border ps-2">
          <TreeList nodes={node.children} {...props} />
        </div>
      )}
    </li>
  );
}
