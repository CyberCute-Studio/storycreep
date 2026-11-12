import type { SignedInBody } from "@storycreep/shared/api";
import { renderHook } from "@testing-library/react";
import { afterEach, expect, it, onTestFinished, vi } from "vitest";
import { createQueryClient, sessionQuery } from "../api";
import { createErrorLog } from "../errors/log";
import { testSession } from "../test/app";
import { fakeApi, testProject } from "../test/fake";
import { holdCopy } from "./clipboard";
import { Documents, keptUnshown } from "./documents";
import { useProjectDocument } from "./react";

// How a page reads a project's document, over the page's documents, which
// the router's context gives it, here without a router.

const context = vi.hoisted(() => ({
  documents: undefined as unknown,
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  useRouter: () => ({ options: { context } }),
}));

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

it("follows the document that it shows, once the page's documents have made it anew, as once the session is of another account that may see the project too", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  fakeApi((operation) => {
    if (operation === "getProject") {
      return testProject;
    }
    throw new Error(`unexpected ${operation}`);
  });
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, testSession);
  const documents = new Documents({ queryClient, log });
  onTestFinished(() => documents.close());
  context.documents = documents;
  const { result, rerender } = renderHook(() =>
    useProjectDocument(testProject.id),
  );
  const first = result.current;
  documents.clear();
  rerender();
  const again = result.current;
  expect(again).not.toBe(first);
  // A document that no page shows is dropped after a while; the one that
  // the page shows is not.
  await vi.advanceTimersByTimeAsync(keptUnshown + 1);
  expect(documents.get(testProject.id)).toBe(again);
});

it("follows the document that the page's documents have now, once they dropped the one that it rendered with before its effect kept it, rather than show that one while they keep another", async () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  fakeApi((operation) => {
    if (operation === "getProject") {
      return testProject;
    }
    throw new Error(`unexpected ${operation}`);
  });
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, testSession);
  const documents = new Documents({ queryClient, log });
  onTestFinished(() => documents.close());
  context.documents = documents;
  // The documents are dropped between the first render and its effect, as
  // once the session became another account's.
  let renders = 0;
  const { result } = renderHook(() => {
    const doc = useProjectDocument(testProject.id);
    if (++renders === 1) {
      documents.clear();
    }
    return doc;
  });
  expect(renders).toBe(2);
  const shown = result.current;
  expect(documents.get(testProject.id)).toBe(shown);
  // The one that the page shows is kept.
  await vi.advanceTimersByTimeAsync(keptUnshown + 1);
  expect(documents.get(testProject.id)).toBe(shown);
});

it("empties the page's clipboard, which holds the account's nodes, once the page is signed in to another account, and once it is signed out", () => {
  const log = createErrorLog();
  const queryClient = createQueryClient(log);
  queryClient.setQueryData(sessionQuery.queryKey, testSession);
  const documents = new Documents({ queryClient, log });
  onTestFinished(() => documents.close());
  const copied = {
    userId: testSession.user_id,
    workspaceId: testProject.workspace_id,
    clipboard: {
      format: "storycreep.clipboard" as const,
      version: 1,
      project_id: testProject.id,
      board_id: "bd_a",
      nodes: [],
      connections: [],
      variables: [],
      components: [],
    },
  };
  holdCopy(documents.clipboard, copied);
  // The session read again, of the same account, keeps it.
  queryClient.setQueryData(sessionQuery.queryKey, { ...testSession });
  expect(documents.clipboard.getState()?.clipboard).toBe(copied.clipboard);
  const other: SignedInBody = { ...testSession, user_id: "usr_other" };
  queryClient.setQueryData(sessionQuery.queryKey, other);
  expect(documents.clipboard.getState()).toBeUndefined();
  holdCopy(documents.clipboard, { ...copied, userId: "usr_other" });
  queryClient.setQueryData(sessionQuery.queryKey, null);
  expect(documents.clipboard.getState()).toBeUndefined();
});
