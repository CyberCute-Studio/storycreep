import { NetworkError, type SignedInBody } from "@storycreep/shared/api";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  accountQuery,
  createQueryClient,
  instanceQuery,
  sessionAgainWait,
  sessionQuery,
} from "../api";
import { createErrorLog } from "../errors/log";
import { projectQuery } from "../project/queries";
import { testAccount, testInstance, testSession } from "../test/app";
import { fakeApi, refused, testProject, testWorkspace } from "../test/fake";
import { workspaceQuery } from "../workspace/queries";
import type { KeptSession } from "./db";
import { askForOwnSession, type KeptReads, keepReads } from "./kept";

// The page's reads of what the device keeps once the server does not answer
// (PRD FR-OFF-02, plan note N97): a request that got no answer at all reads
// the device, so that a page that loads offline starts as the account last
// signed in, with its pinned projects; any answer of the server's, a
// refusal too, is the server's.

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const kept: KeptSession = {
  user_id: testSession.user_id,
  session: { ...testSession, csrf_token: "" },
  account: testAccount,
  instance: testInstance,
  saved_at: "2026-11-26T10:00:00Z",
};

const workspace = {
  id: testWorkspace.id,
  name: testWorkspace.name,
  slug: testWorkspace.slug,
  default_locale: "en",
  role: "owner" as const,
  membership: true,
};

const reads: KeptReads = {
  session: async () => kept,
  project: async (userId, projectId) =>
    userId === kept.user_id && projectId === testProject.id
      ? testProject
      : undefined,
  workspace: async (userId, workspaceId) =>
    userId === kept.user_id && workspaceId === workspace.id
      ? workspace
      : undefined,
};

function client(withReads = true) {
  const queryClient = createQueryClient(createErrorLog());
  queryClient.setDefaultOptions({ queries: { retry: false } });
  if (withReads) {
    keepReads(queryClient, reads);
  }
  return queryClient;
}

/** Makes the browser say that it has no connection, as the reads read it. */
function offline() {
  vi.spyOn(navigator, "onLine", "get").mockReturnValue(false);
}

describe("a page that the server does not answer", () => {
  it("reads the session, the account, the instance, the project, and its workspace that the device keeps", async () => {
    offline();
    fakeApi(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const queryClient = client();
    expect(await queryClient.fetchQuery(sessionQuery)).toEqual(kept.session);
    expect(await queryClient.fetchQuery(accountQuery)).toEqual(testAccount);
    expect(await queryClient.fetchQuery(instanceQuery)).toEqual(testInstance);
    expect(await queryClient.fetchQuery(projectQuery(testProject.id))).toEqual(
      testProject,
    );
    expect(
      await queryClient.fetchQuery(workspaceQuery(testWorkspace.id)),
    ).toEqual(workspace);
    // A project that the device does not keep is not there to read.
    await expect(
      queryClient.fetchQuery(projectQuery("prj_other")),
    ).rejects.toThrow(NetworkError);
  });

  it("reads the device's account only of the account that the page is signed in to", async () => {
    offline();
    fakeApi(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const queryClient = client();
    const kim: SignedInBody = { ...testSession, user_id: "usr_kim" };
    queryClient.setQueryData(sessionQuery.queryKey, kim);
    await expect(queryClient.fetchQuery(accountQuery)).rejects.toThrow(
      NetworkError,
    );
  });

  it("is the server's where the server answers, a refusal too, and fails as before without the device's", async () => {
    fakeApi(() => {
      throw refused(401, { code: "unauthenticated" });
    });
    expect(await client().fetchQuery(sessionQuery)).toBeNull();
    await expect(client().fetchQuery(accountQuery)).rejects.toMatchObject({
      status: 401,
    });
    fakeApi(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    await expect(client(false).fetchQuery(sessionQuery)).rejects.toThrow(
      NetworkError,
    );
  });
});

describe("the instance that the device keeps", () => {
  it("is the page's until it loads anew, as the server's is, once the browser is online too", async () => {
    let answers = false;
    let calls = 0;
    fakeApi(() => {
      calls++;
      if (!answers) {
        throw new NetworkError(new TypeError("Failed to fetch"));
      }
      return { ...testInstance, default_locale: "de" };
    });
    const queryClient = createQueryClient(createErrorLog());
    queryClient.setDefaultOptions({ queries: { retry: false } });
    keepReads(queryClient, {
      ...reads,
      session: async () => ({ ...kept, instance: { ...testInstance } }),
    });
    const onLine = vi.spyOn(navigator, "onLine", "get").mockReturnValue(false);
    expect((await queryClient.fetchQuery(instanceQuery)).default_locale).toBe(
      "en",
    );
    // Offline, the device's is what there is.
    await queryClient.fetchQuery(instanceQuery);
    expect(calls).toBe(1);
    onLine.mockReturnValue(true);
    answers = true;
    expect((await queryClient.fetchQuery(instanceQuery)).default_locale).toBe(
      "en",
    );
    expect(calls).toBe(1);
  });
});

describe("a page's session, where a request of it gets no answer", () => {
  it("is asked for once more a moment after, while the browser says that it is online, before the device's is taken", async () => {
    vi.useFakeTimers();
    let answered = 0;
    fakeApi((operation) => {
      if (operation !== "getSession") {
        throw new Error(`unexpected ${operation}`);
      }
      answered++;
      if (answered === 1) {
        throw new NetworkError(new TypeError("Failed to fetch"));
      }
      return testSession;
    });
    const session = client().fetchQuery(sessionQuery);
    await vi.advanceTimersByTimeAsync(sessionAgainWait);
    expect(await session).toEqual(testSession);
    expect(answered).toBe(2);
  });

  it("is the device's where the server does not answer that either", async () => {
    vi.useFakeTimers();
    fakeApi(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const session = client().fetchQuery(sessionQuery);
    await vi.advanceTimersByTimeAsync(sessionAgainWait);
    expect(await session).toEqual(kept.session);
  });

  it("is kept where the page holds its own, as the server answered it, rather than taken for the device's", async () => {
    offline();
    fakeApi(() => {
      throw new NetworkError(new TypeError("Failed to fetch"));
    });
    const queryClient = client();
    queryClient.setQueryData(sessionQuery.queryKey, testSession);
    expect(
      await queryClient.fetchQuery({ ...sessionQuery, staleTime: 0 }),
    ).toEqual(testSession);
    // The device's, which the page started from, is the device's still.
    queryClient.setQueryData(sessionQuery.queryKey, kept.session);
    expect(
      await queryClient.fetchQuery({ ...sessionQuery, staleTime: 0 }),
    ).toEqual(kept.session);
  });
});

describe("a page that holds the session that the device keeps", () => {
  it("asks the server for its own while the browser says that it is online, until it has it", () => {
    vi.useFakeTimers();
    let held = true;
    let connected = false;
    const check = vi.fn();
    const stop = askForOwnSession(
      () => held,
      check,
      () => connected,
      1_000,
    );
    vi.advanceTimersByTime(1_000);
    expect(check).not.toHaveBeenCalled();
    // As once the server answers again after a restart, which no "online"
    // event tells of.
    connected = true;
    vi.advanceTimersByTime(1_000);
    expect(check).toHaveBeenCalledOnce();
    held = false;
    vi.advanceTimersByTime(3_000);
    expect(check).toHaveBeenCalledOnce();
    held = true;
    stop();
    vi.advanceTimersByTime(3_000);
    expect(check).toHaveBeenCalledOnce();
  });
});
