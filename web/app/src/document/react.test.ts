import { renderHook } from "@testing-library/react";
import { afterEach, expect, it, onTestFinished, vi } from "vitest";
import { createQueryClient, sessionQuery } from "../api";
import { createErrorLog } from "../errors/log";
import { testSession } from "../test/app";
import { fakeApi, testProject } from "../test/fake";
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
