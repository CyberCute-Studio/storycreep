// The appliers of the commands of variables (PRD FR-VAR-01, FR-VAR-02), as
// package variable applies them: a value is stored in its canonical form,
// which is the number that JavaScript reads, but -0, which is 0; a key of
// order that the payload leaves out is the server's to pick, after every
// child of the folder, those in the trash too, and the variables of boards
// in the trash; and a command that changes what expressions can name, as a
// variable's key, type, scope, or its going to the trash, has the server
// compile the project's expressions that name it again (renames).

import type { Variable } from "@storycreep/shared/api";
import { type Appliers, canonical } from "../apply";
import type { Draft } from "../doc";
import {
  folderChildren,
  idOrNone,
  keyAfter,
  placed,
  renames,
  withField,
} from "./node";

type Types =
  `variable.${"create" | "rename" | "describe" | "set_default" | "retype" | "set_scope" | "set_reset_on_enter" | "move" | "delete" | "restore"}`;

type Value = Variable["default"];

/** Returns value in its canonical form, as the server stores a value of a variable or of an attribute definition. */
export function canonicalValue<V extends Value>(value: V): V {
  return (value === 0 ? 0 : value) as V;
}

/** Returns the zero value of type, which the server gives a variable that is created or retyped without a default. */
function zeroOf(type: Variable["type"]): Value {
  return type === "bool" ? false : type === "string" ? "" : 0;
}

/** Changes the variable of id with change, if draft holds it. */
function changeVariable(
  draft: Draft,
  id: string,
  change: (variable: Variable) => Variable,
  sure = true,
): void {
  const variable = draft.get("variables", canonical(id));
  if (variable !== undefined) {
    draft.put(
      "variables",
      { ...change(variable), updated_at: draft.time },
      sure,
    );
  }
}

export const variableAppliers: Appliers<Types> = {
  "variable.create": (draft, p) => {
    const folderId = idOrNone(p.folder_id);
    const boardId = p.scope === "board" ? idOrNone(p.board_id) : undefined;
    draft.put(
      "variables",
      {
        id: canonical(p.id),
        project_id: draft.project?.id ?? "",
        scope: p.scope,
        ...(boardId === undefined ? {} : { board_id: boardId }),
        ...(folderId === undefined ? {} : { folder_id: folderId }),
        order:
          p.order ??
          keyAfter(
            folderChildren(draft, "variable", folderId).map(
              (child) => child.order,
            ),
          ),
        key: p.key,
        type: p.type,
        default: canonicalValue(p.default ?? zeroOf(p.type)),
        description: p.description ?? "",
        reset_on_enter: p.reset_on_enter ?? false,
        rev: 1,
        created_at: draft.time,
        updated_at: draft.time,
      },
      p.order !== undefined,
    );
    renames(draft);
  },
  // The expressions that name the variable are written anew, which the
  // store reads, as its operation's revisions name them.
  "variable.rename": (draft, p) => {
    changeVariable(draft, p.id, (v) => ({ ...v, key: p.key }));
    renames(draft);
  },
  "variable.describe": (draft, p) =>
    changeVariable(draft, p.id, (v) => ({ ...v, description: p.description })),
  "variable.set_default": (draft, p) =>
    changeVariable(draft, p.id, (v) => ({
      ...v,
      default: canonicalValue(p.default),
    })),
  // The default is the payload's, of the new type, or its zero value: the
  // old one is not converted.
  "variable.retype": (draft, p) => {
    changeVariable(draft, p.id, (v) => ({
      ...v,
      type: p.type,
      default: canonicalValue(p.default ?? zeroOf(p.type)),
    }));
    renames(draft);
  },
  "variable.set_scope": (draft, p) => {
    changeVariable(draft, p.id, (v) =>
      withField(
        { ...v, scope: p.scope },
        "board_id",
        p.scope === "board" ? idOrNone(p.board_id) : undefined,
      ),
    );
    renames(draft);
  },
  "variable.set_reset_on_enter": (draft, p) =>
    changeVariable(draft, p.id, (v) => ({
      ...v,
      reset_on_enter: p.reset_on_enter,
    })),
  "variable.move": (draft, p) => {
    const variable = draft.get("variables", canonical(p.id));
    if (variable === undefined) {
      return;
    }
    const folderId = idOrNone(p.folder_id);
    const { order, sure } =
      p.order !== undefined
        ? { order: p.order, sure: true }
        : variable.folder_id === folderId
          ? placed(variable, folderChildren(draft, "variable", folderId))
          : {
              order: keyAfter(
                folderChildren(draft, "variable", folderId).map((c) => c.order),
              ),
              sure: false,
            };
    draft.put(
      "variables",
      {
        ...withField(variable, "folder_id", folderId),
        order,
        updated_at: draft.time,
      },
      sure,
    );
  },
  "variable.delete": (draft, p) => {
    draft.remove("variables", canonical(p.id));
    renames(draft);
  },
  // The variable in the trash, and the folders that its restoration brings
  // back, are none that the document holds: the store reads them, as its
  // operation's revisions name them.
  "variable.restore": (draft) => renames(draft),
};
