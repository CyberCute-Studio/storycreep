// What the expressions of a board can name, for a page, as the project's
// document has it now (scope.ts): the same object until the document's
// variables, components, component types, boards, or nodes change, and a
// Project, which every editor of the page shares, made anew only once the
// variables, the components, or their types do, and none until the page has
// read them.

import { useEffect, useMemo, useState } from "react";
import { useDocumentState, useProjectDocument } from "../document/react";
import { type Scope, sharedProjectOf } from "./scope";

/** The parts of the document that a Scope's Project is of. */
const scopeParts = ["variables", "components"] as const;

/**
 * Returns what the expressions of the board of boardId, of the project of
 * projectId, can name. A page that has not read the project's variables or
 * its components has the document read them, and the editor checks a
 * source's syntax alone until it has, rather than find every name unknown
 * of a project that has none yet; a read that failed, which the document
 * records, leaves it checking the syntax alone, and the server the rest.
 */
export function useScope(projectId: string, boardId: string): Scope {
  const doc = useProjectDocument(projectId);
  const variables = useDocumentState(doc, (s) => s.view.variables);
  const components = useDocumentState(doc, (s) => s.view.components);
  const componentTypes = useDocumentState(doc, (s) => s.view.componentTypes);
  const boards = useDocumentState(doc, (s) => s.view.boards);
  const nodes = useDocumentState(doc, (s) => s.view.nodes);
  const gone = useDocumentState(doc, (s) => s.view.gone);
  // The reads of the parts that ended, which show the scope anew: the
  // document tells of what a read brought before it counts it as read, and
  // of nothing where it brought nothing, as of a project of no variables.
  const [, setReads] = useState(0);
  useEffect(() => {
    let ended = false;
    Promise.all(scopeParts.map((part) => doc.load(part))).then(
      () => {
        if (!ended) {
          setReads((n) => n + 1);
        }
      },
      () => undefined,
    );
    return () => {
      ended = true;
    };
  }, [doc]);
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
