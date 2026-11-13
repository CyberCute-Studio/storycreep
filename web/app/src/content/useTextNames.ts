// The names that the rich text of a page shows, and what its { and @ offer
// (PRD FR-TEXT-02, FR-TEXT-03, plan note N73), as the project's document
// has them now (textNames.ts), each the same object until what it is made
// of changes, as the editor shows its references again whenever its names
// are another object.

import { useEffect, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useDocumentState, useProjectDocument } from "../document/react";
import type { RichTextChoices } from "../richtext/choices";
import type { RichTextNames } from "../richtext/extensions";
import { type NameSources, textChoices, textNames } from "./textNames";

/**
 * Returns what the names of the texts of the project of projectId are read
 * from, as its document has them. A page that has not read the project's
 * variables or its components, as the components' page has not read the
 * variables, has the document read them, so that its texts name what they
 * refer to; a read that failed, which the document records, leaves them
 * named as what they are.
 */
function useNameSources(projectId: string): NameSources {
  const doc = useProjectDocument(projectId);
  const variables = useDocumentState(doc, (s) => s.view.variables);
  const components = useDocumentState(doc, (s) => s.view.components);
  const componentTypes = useDocumentState(doc, (s) => s.view.componentTypes);
  useEffect(() => {
    for (const part of ["variables", "components"] as const) {
      if (!doc.holds(part)) {
        void doc.load(part).catch(() => undefined);
      }
    }
  }, [doc]);
  return useMemo(
    () => ({ variables, components, componentTypes }),
    [variables, components, componentTypes],
  );
}

/** Returns the names that the rich text of the project of projectId shows, of a text of the board of boardId, or of none, as a component's description. */
export function useTextNames(
  projectId: string,
  boardId?: string,
): RichTextNames {
  const view = useNameSources(projectId);
  return useMemo(() => textNames(view, boardId), [view, boardId]);
}

/** Returns what the { and @ of a text of the project of projectId offer, of a text of the board of boardId, or of none, as a component's description, which only a text that may be changed needs. */
export function useTextChoices(
  projectId: string,
  boardId?: string,
): RichTextChoices {
  const { t } = useTranslation();
  const view = useNameSources(projectId);
  return useMemo(() => textChoices(view, boardId, t), [view, boardId, t]);
}
