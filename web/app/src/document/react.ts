// How a page reads a project's document (store.ts): the page's documents
// come with the router's context, so that a route's loader reads a part of
// the project before its page shows, and a page reads what it shows of the
// document with a selector, which renders it again only once that changes,
// as the document's maps are shared between versions where nothing of them
// changed.

import type { Board, Folder } from "@storycreep/shared/api";
import { useRouter } from "@tanstack/react-router";
import { useEffect, useMemo, useReducer } from "react";
import { useStore } from "zustand";
import type { Documents } from "./documents";
import type { DocumentState, ProjectDocument } from "./store";

/** Returns the page's documents. */
export function useDocuments(): Documents {
  return useRouter().options.context.documents;
}

/**
 * Returns the document of projectId, which the page follows while the
 * component that reads it is shown: the one that it shows, which is made
 * anew once the page's documents have dropped the one before, as once the
 * session is of another account that may see the project too, and which
 * the component renders again with once they drop the one that it rendered
 * with before its effect has kept it (Documents.acquire).
 */
export function useProjectDocument(projectId: string): ProjectDocument {
  const documents = useDocuments();
  const [, dropped] = useReducer((renders: number) => renders + 1, 0);
  const doc = documents.get(projectId);
  useEffect(() => {
    const release = documents.acquire(doc);
    if (release === undefined) {
      dropped();
    }
    return release;
  }, [documents, doc]);
  return doc;
}

/** Returns what select reads of the state of doc, as it changes. */
export function useDocumentState<T>(
  doc: ProjectDocument,
  select: (state: DocumentState) => T,
): T {
  return useStore(doc.store, select);
}

/** What the tree of a project's boards is built from. */
export interface TreeData {
  folders: Folder[];
  boards: Board[];
}

/** Returns the folders of boards and the boards of doc, which the tree of its boards shows. */
export function useTree(doc: ProjectDocument): TreeData {
  const folders = useStore(doc.store, (state) => state.view.folders);
  const boards = useStore(doc.store, (state) => state.view.boards);
  return useMemo(
    () => ({
      folders: [...folders.values()].filter((f) => f.kind === "board"),
      boards: [...boards.values()],
    }),
    [folders, boards],
  );
}

/** Returns the board of id of doc, or undefined once it has none, as once it went to the trash. */
export function useBoard(doc: ProjectDocument, id: string): Board | undefined {
  return useStore(doc.store, (state) => state.view.boards.get(id));
}
