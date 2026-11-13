// What the expressions of a board can name, for a page, as the project's
// document has it now (scope.ts): the same object until the document's
// variables, components, component types, boards, or nodes change, and a
// Project made anew only once the variables, the components, or their
// types do.

import { useEffect, useMemo } from "react";
import { useDocumentState, useProjectDocument } from "../document/react";
import { projectOf, type Scope } from "./scope";

/**
 * Returns what the expressions of the board of boardId, of the project of
 * projectId, can name. A page that has not read the project's variables or
 * its components has the document read them, so that the editor checks a
 * source against them; a read that failed, which the document records,
 * leaves the editor checking against what it holds.
 */
export function useScope(projectId: string, boardId: string): Scope {
  const doc = useProjectDocument(projectId);
  const variables = useDocumentState(doc, (s) => s.view.variables);
  const components = useDocumentState(doc, (s) => s.view.components);
  const componentTypes = useDocumentState(doc, (s) => s.view.componentTypes);
  const boards = useDocumentState(doc, (s) => s.view.boards);
  const nodes = useDocumentState(doc, (s) => s.view.nodes);
  const gone = useDocumentState(doc, (s) => s.view.gone);
  useEffect(() => {
    for (const part of ["variables", "components"] as const) {
      if (!doc.holds(part)) {
        void doc.load(part).catch(() => undefined);
      }
    }
  }, [doc]);
  const project = useMemo(
    () =>
      projectOf({
        variables,
        components,
        componentTypes,
        boards: new Map(),
        nodes: new Map(),
        gone: new Map(),
      }),
    [variables, components, componentTypes],
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
