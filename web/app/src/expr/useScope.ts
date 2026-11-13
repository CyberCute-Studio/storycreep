// What the expressions of a board can name, for a page, as the project's
// document has it now (scope.ts): the same object until the document's
// variables, components, component types, boards, or nodes change, and a
// Project, which every editor of the page shares, made anew only once the
// variables, the components, or their types do, and none until the page has
// read them.

import { useEffect, useMemo, useState } from "react";
import { useDocumentState, useProjectDocument } from "../document/react";
import type { ProjectDocument } from "../document/store";
import { type Scope, sharedProjectOf } from "./scope";

/** The parts of the document that a Scope's Project is of. */
const scopeParts = ["variables", "components"] as const;

/** Has doc read the parts of a Scope, of those that it does not hold, and calls ended once their reads have ended, unless the function returned is called first; of a read that fails, nothing. */
function awaitReads(doc: ProjectDocument, ended: () => void): () => void {
  let stopped = false;
  Promise.all(scopeParts.map((part) => doc.load(part))).then(
    () => {
      if (!stopped) {
        ended();
      }
    },
    () => undefined,
  );
  return () => {
    stopped = true;
  };
}

/**
 * Returns what the expressions of the board of boardId, of the project of
 * projectId, can name. A page that has not read the project's variables or
 * its components has the document read them, and the editor checks a
 * source's syntax alone until it has, rather than find every name unknown
 * of a project that has none yet; a read that failed, which the document
 * records, leaves it checking the syntax alone, and the server the rest,
 * until another asks for the parts anew, as another page's editor, and
 * that read ends.
 */
export function useScope(projectId: string, boardId: string): Scope {
  const doc = useProjectDocument(projectId);
  const variables = useDocumentState(doc, (s) => s.view.variables);
  const components = useDocumentState(doc, (s) => s.view.components);
  const componentTypes = useDocumentState(doc, (s) => s.view.componentTypes);
  const boards = useDocumentState(doc, (s) => s.view.boards);
  const nodes = useDocumentState(doc, (s) => s.view.nodes);
  const gone = useDocumentState(doc, (s) => s.view.gone);
  // Whether the document holds the parts, read or being read, as once
  // another asks for them anew after the read that this asked for failed.
  const held = useDocumentState(doc, (s) =>
    scopeParts.every((part) => s.view.parts.has(part)),
  );
  // The reads of the parts that ended, which show the scope anew: the
  // document tells of what a read brought before it counts it as read, and
  // of nothing where it brought nothing, as of a project of no variables.
  const [, setReads] = useState(0);
  // This asks for the parts once; a read that failed it asks for no more,
  // but awaits the one that another asks for, which load gives it rather
  // than read them again, as it gives every read of a part that it holds.
  useEffect(() => awaitReads(doc, () => setReads((n) => n + 1)), [doc]);
  useEffect(
    () => (held ? awaitReads(doc, () => setReads((n) => n + 1)) : undefined),
    [doc, held],
  );
  const read = scopeParts.every((part) => doc.hasRead(part));
  const project = useMemo(
    () =>
      read
        ? sharedProjectOf({
            variables,
            components,
            componentTypes,
            boards: new Map(),
            nodes: new Map(),
            gone: new Map(),
          })
        : undefined,
    [read, variables, components, componentTypes],
  );
  return useMemo(
    () => ({
      boardId,
      view: { variables, components, componentTypes, boards, nodes, gone },
      project,
    }),
    [
      boardId,
      variables,
      components,
      componentTypes,
      boards,
      nodes,
      gone,
      project,
    ],
  );
}
