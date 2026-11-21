import {
  type Diff,
  type Finding,
  NetworkError,
  type Report,
  type Snapshot,
} from "@storycreep/shared/api";
import { newId } from "@storycreep/shared/ids";
import { focusManager } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, onTestFinished, vi } from "vitest";
import { readProjectAgain } from "./project/queries";
import { readReleasesAgain, releaseReportQuery } from "./releases/queries";
import { renderApp, testSession } from "./test/app";
import { fakeApi, refused, startPage, testProject } from "./test/fake";
import { fakeRealtime } from "./test/realtime";
import { harbourId, releaseFiles, towerId, zipOf } from "./test/release";
import { validationKey } from "./validation/queries";

// A project's releases (PRD FR-SNAP-01, FR-SNAP-02, FR-SNAP-04, FR-DEP-04,
// plan note N88), as the router shows them, over a fake of the API's client.

afterEach(() => {
  vi.restoreAllMocks();
});

const olderId = newId("snapshot");
const newerId = newId("snapshot");

/** A snapshot of the test project. */
function snapshot(more: Partial<Snapshot> & Pick<Snapshot, "id">): Snapshot {
  return {
    acknowledged_seq: 1,
    note: "Harbour intro",
    project_id: testProject.id,
    published_at: "2026-11-10T10:00:00Z",
    published_by: "usr_test",
    published_by_name: "Jo",
    status: "ready",
    version: "1.3.0",
    counts: {
      boards: 2,
      components: 1,
      connections: 3,
      nodes: 6,
      variables: 2,
    },
    ...more,
  };
}

const older = snapshot({ id: olderId, version: "1.3.0" });
const newer = snapshot({ id: newerId, version: "1.4.0", note: "Keeper gate" });

/** A warning of the node Greet. */
function warning(more: Partial<Finding> = {}): Finding {
  return {
    code: "dead_end",
    severity: "warning",
    message: "Nothing leaves this node.",
    entity_type: "node",
    entity_id: "nd_a",
    board_id: "bd_a",
    board_name: "Harbour",
    node_id: "nd_a",
    node_title: "Greet",
    ...more,
  };
}

/** The report of findings, as of seq. */
function report(findings: Finding[], seq = 7): Report {
  return {
    seq,
    findings,
    muted: [],
    counts: {
      errors: findings.filter((f) => f.severity === "error").length,
      warnings: findings.filter((f) => f.severity === "warning").length,
      infos: 0,
      muted: 0,
    },
  };
}

const noCounts = { added: 0, cosmetic: 0, modified: 0, moved: 0, removed: 0 };

/** A diff of nothing but what more says. */
function diffOf(more: Partial<Diff> = {}): Diff {
  return {
    boards: [],
    from: { kind: "snapshot", contentHash: "", id: olderId, version: "1.3.0" },
    to: { kind: "snapshot", contentHash: "", id: newerId, version: "1.4.0" },
    schemaVersion: 1,
    truncated: false,
    project: {
      attributes: [],
      componentTypes: [],
      components: [],
      fields: [],
      omitted: 0,
      variables: [],
    },
    summary: {
      ...noCounts,
      kinds: {
        attribute: noCounts,
        board: noCounts,
        branch_output: noCounts,
        component: noCounts,
        component_type: noCounts,
        connection: noCounts,
        node: noCounts,
        project: noCounts,
        variable: noCounts,
      },
    },
    ...more,
  };
}

interface Answers {
  role?: (typeof testProject)["role"];
  list?: () => Snapshot[];
  validation?: () => Report;
  /** The report stored with a release. */
  stored?: () => Report;
  diff?: () => Diff | Promise<Diff>;
  publish?: (body: unknown) => Snapshot;
  zip?: Uint8Array<ArrayBuffer>;
  /** The cursor of the list's next page, of which it has none otherwise. */
  nextCursor?: string;
  /** The version that publishing proposes. */
  next?: () => unknown;
}

/** Answers the pages of the project's releases. */
function releasesPage(answers: Answers = {}) {
  const list = answers.list ?? (() => [newer, older]);
  return fakeApi(async (operation, input) => {
    switch (operation) {
      case "getProject":
        return { ...testProject, role: answers.role ?? "owner" };
      case "listSnapshots": {
        const { query } = input as {
          query?: { status?: string; limit?: number };
        };
        const items = list().filter(
          (s) => query?.status === undefined || s.status === query.status,
        );
        return {
          items: items.slice(0, query?.limit ?? items.length),
          next_cursor:
            query?.status === undefined ? (answers.nextCursor ?? null) : null,
        };
      }
      case "getSnapshot": {
        const { path } = input as { path: { snapshot_id: string } };
        const found = list().find((s) => s.id === path.snapshot_id);
        if (found === undefined) {
          throw refused(404, { code: "not_found" });
        }
        return found;
      }
      case "getSnapshotValidation":
        return answers.stored?.() ?? report([warning()]);
      case "getNextSnapshotVersion":
        return (
          answers.next?.() ?? { version: "1.4.1", latest_version: "1.4.0" }
        );
      case "validateProject":
        return (answers.validation ?? (() => report([])))();
      case "diffSnapshots":
        return (answers.diff ?? (() => diffOf()))();
      case "publishSnapshot":
        return answers.publish?.((input as { body: unknown }).body);
      case "exportSnapshot":
        return new Response(
          answers.zip ?? (await zipOf(releaseFiles())),
        ).blob();
      case "listVariables":
      case "listFolders":
      case "listBoards":
        return { items: [], next_cursor: null };
    }
    const start = startPage(operation);
    if (start === undefined) {
      throw new Error(`unexpected ${operation}`);
    }
    return start;
  });
}

/** Has calls answer an export with what answer resolves to, a whole answer of the client's. */
function answerExport(
  calls: ReturnType<typeof fakeApi>,
  answer: () => Promise<unknown>,
) {
  const rest = calls.getMockImplementation() as (...a: unknown[]) => unknown;
  calls.mockImplementation(((operation: string, ...more: unknown[]) =>
    operation === "exportSnapshot"
      ? answer()
      : rest(operation, ...more)) as never);
}

/** Renders the list of releases, and waits for its heading. */
async function openReleases() {
  const view = renderApp(`/projects/${testProject.id}/releases`, {
    session: testSession,
  });
  await screen.findByRole("heading", { level: 1, name: "Releases" });
  return view;
}

/** Opens the dialog that publishes, and returns it once it has read the draft's report. */
async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Publish" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Publish a release",
  });
  await within(dialog).findByText(/\d+ errors?, \d+ warnings?/);
  return dialog;
}

/** A refusal of a version as the server words it, at the version's field. */
const notNewerBody = {
  code: "version_not_newer",
  message: "This version is not newer than the project's latest release.",
  details: {
    latest_version: "1.4.1",
    proposed_version: "1.4.2",
    errors: [
      {
        location: "body.version",
        message: "is not greater than 1.4.1, the newest version",
      },
    ],
  },
};

/** Returns the release's files with the JSON of the file at path changed by change. */
function filesWith(path: string, change: (file: never) => void) {
  const files = releaseFiles();
  const file = JSON.parse(new TextDecoder().decode(files.get(path))) as never;
  change(file);
  files.set(
    path,
    new TextEncoder().encode(JSON.stringify(file)) as Uint8Array<ArrayBuffer>,
  );
  return files;
}

/** Returns the calls of operation that calls made. */
function callsOf(calls: ReturnType<typeof fakeApi>, operation: string) {
  return calls.mock.calls.filter(([op]) => op === operation);
}

describe("the list of a project's releases", () => {
  it("shows each with its status, facts, and what can be done with it", async () => {
    releasesPage({
      list: () => [
        snapshot({
          id: newId("snapshot"),
          version: "1.5.0",
          status: "failed",
          failure_code: "validation_changed",
          counts: undefined,
          published_by: undefined,
          published_by_name: undefined,
        }),
        snapshot({
          id: newId("snapshot"),
          version: "1.4.1",
          status: "building",
          counts: undefined,
        }),
        newer,
        older,
      ],
    });
    await openReleases();
    expect(document.title).toContain("Releases of Harbour");
    expect(screen.getByText("Failed")).toBeDefined();
    expect(screen.getByText("Building")).toBeDefined();
    expect(screen.getByText(/gained a warning/)).toBeDefined();
    expect(screen.getByText(/Published by a former member/)).toBeDefined();
    expect(screen.getByText("Keeper gate")).toBeDefined();
    expect(
      screen.getAllByText(
        "2 boards · 6 nodes · 3 connections · 2 variables · 1 component",
      ),
    ).toHaveLength(2);
    // Only a ready release has files to download or compare.
    expect(
      screen.queryByRole("button", { name: "Download 1.5.0 as a zip" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Download 1.4.0 as a zip" }),
    ).toBeDefined();
    const compare = screen
      .getByRole("link", { name: "Compare 1.4.0 with 1.3.0" })
      .getAttribute("href");
    expect(compare).toContain(`from=${olderId}`);
    expect(compare).toContain(`to=${newerId}`);
    // The oldest has none before it, and compares with the draft.
    expect(
      screen
        .getByRole("link", { name: "Compare 1.3.0 with the draft" })
        .getAttribute("href"),
    ).toContain("to=draft");
  });

  it("offers publishing only to a role that may publish", async () => {
    releasesPage({ role: "viewer" });
    await openReleases();
    expect(screen.queryByRole("button", { name: "Publish" })).toBeNull();
  });

  it("reads a building release again until its build ends", async () => {
    let status: Snapshot["status"] = "building";
    releasesPage({
      list: () => [snapshot({ id: newerId, version: "1.4.0", status })],
    });
    await openReleases();
    expect(screen.getByText("Building")).toBeDefined();
    status = "ready";
    expect(
      await screen.findByText("Ready", undefined, { timeout: 4000 }),
    ).toBeDefined();
  });

  it("follows a build by its snapshot, without reading the list's pages again", async () => {
    const calls = releasesPage({
      list: () => [
        snapshot({ id: newerId, version: "1.4.0", status: "building" }),
        older,
      ],
    });
    await openReleases();
    const listReads = () =>
      callsOf(calls, "listSnapshots").filter(
        ([, input]) =>
          (input as { query?: { status?: string } }).query?.status ===
          undefined,
      ).length;
    const before = listReads();
    await waitFor(
      () =>
        expect(callsOf(calls, "getSnapshot").length).toBeGreaterThanOrEqual(2),
      { timeout: 4000 },
    );
    expect(listReads()).toBe(before);
  });

  it("links the last release that it has read to the one before it, while older ones are unread", async () => {
    releasesPage({ list: () => [newer], nextCursor: "next" });
    await openReleases();
    const compare = screen
      .getByRole("link", {
        name: "Compare 1.4.0 with the release before it, or with the draft if it has none",
      })
      .getAttribute("href");
    expect(compare).toContain(`to=${newerId}`);
    expect(compare).not.toContain("from=");
  });

  it("reads the newest ready release again once a build ends", async () => {
    let status: Snapshot["status"] = "building";
    const calls = releasesPage({
      list: () => [snapshot({ id: newerId, version: "1.4.0", status }), older],
    });
    await openReleases();
    await openDialog(userEvent.setup());
    const latestReads = () =>
      callsOf(calls, "listSnapshots").filter(
        ([, input]) =>
          (input as { query?: { limit?: number } }).query?.limit === 1,
      ).length;
    await waitFor(() => expect(latestReads()).toBe(1));
    status = "ready";
    await waitFor(() => expect(latestReads()).toBeGreaterThan(1), {
      timeout: 4000,
    });
  });

  it("downloads a release's zip under the server's name", async () => {
    const calls = releasesPage();
    answerExport(calls, async () => ({
      status: 200,
      body: await new Response(await zipOf(releaseFiles())).blob(),
      response: new Response(null, {
        headers: {
          "Content-Disposition": "attachment; filename=harbour-1.4.0.zip",
        },
      }),
    }));
    const created = vi.fn((_: Blob) => "blob:release");
    const { createObjectURL, revokeObjectURL } = URL;
    onTestFinished(() => {
      URL.createObjectURL = createObjectURL;
      URL.revokeObjectURL = revokeObjectURL;
    });
    URL.createObjectURL = created;
    URL.revokeObjectURL = vi.fn();
    const clicked: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicked.push(this.download);
    });
    await openReleases();
    const user = userEvent.setup();
    await user.click(
      screen.getByRole("button", { name: "Download 1.4.0 as a zip" }),
    );
    await waitFor(() => expect(created).toHaveBeenCalledOnce());
    expect(clicked).toEqual(["harbour-1.4.0.zip"]);
    // The release's own files, at its own version of the format.
    expect(callsOf(calls, "exportSnapshot")[0][1]).toEqual({
      path: { project_id: testProject.id, snapshot_id: newerId },
    });
  });

  it("says why a download was refused", async () => {
    const calls = releasesPage();
    answerExport(calls, () =>
      Promise.reject(
        refused(429, { code: "rate_limited" }, { "Retry-After": "30" }),
      ),
    );
    await openReleases();
    const user = userEvent.setup();
    await user.click(
      screen.getByRole("button", { name: "Download 1.4.0 as a zip" }),
    );
    expect(
      await screen.findByText("Too many requests. Try again in 30 seconds."),
    ).toBeDefined();
  });
});

describe("publishing a release", () => {
  // Verifies: FR-DEP-04/AC1
  it("cannot publish a draft with an error", async () => {
    const calls = releasesPage({
      validation: () =>
        report([
          warning({
            code: "expr_invalid",
            severity: "error",
            message: "The condition does not compile.",
          }),
        ]),
    });
    await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    expect(within(dialog).getByText("1 error, 0 warnings")).toBeDefined();
    expect(
      within(dialog).getByText("The condition does not compile."),
    ).toBeDefined();
    expect(
      within(dialog).getByRole("link", {
        name: "Open the project's validation",
      }),
    ).toBeDefined();
    const submit = within(dialog).getByRole("button", { name: "Publish" });
    expect((submit as HTMLButtonElement).disabled).toBe(true);
    await user.type(
      within(dialog).getByRole("textbox", { name: /Changelog note/ }),
      "A note{Enter}",
    );
    expect(callsOf(calls, "publishSnapshot")).toHaveLength(0);
  });

  it("publishes the proposed version once its warnings are acknowledged", async () => {
    const bodies: unknown[] = [];
    const published = snapshot({
      id: newId("snapshot"),
      version: "1.4.1",
      status: "building",
      counts: undefined,
    });
    let list = [newer, older];
    releasesPage({
      list: () => list,
      validation: () =>
        report([
          warning({
            target: { type: "node", id: "nd_b" },
            field: "target",
          }),
        ]),
      publish: (body) => {
        bodies.push(body);
        list = [published, newer, older];
        return published;
      },
    });
    await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const version = within(dialog).getByRole("textbox", { name: /Version/ });
    await waitFor(() =>
      expect((version as HTMLInputElement).value).toBe("1.4.1"),
    );
    const submit = within(dialog).getByRole("button", { name: "Publish" });
    expect((submit as HTMLButtonElement).disabled).toBe(true);
    await user.click(
      within(dialog).getByRole("checkbox", {
        name: "I have read this warning and want to publish with it",
      }),
    );
    expect((submit as HTMLButtonElement).disabled).toBe(false);

    // The note is required.
    await user.click(submit);
    expect(
      await within(dialog).findByText(
        "Write a note of what this release changes.",
      ),
    ).toBeDefined();
    expect(bodies).toHaveLength(0);

    await user.type(
      within(dialog).getByRole("textbox", { name: /Changelog note/ }),
      "  Harbour intro  ",
    );
    await user.click(submit);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(bodies).toEqual([
      {
        version: "1.4.1",
        note: "Harbour intro",
        acknowledged_seq: 7,
        acknowledged_warnings: [
          {
            code: "dead_end",
            entity_id: "nd_a",
            field: "target",
            target: { type: "node", id: "nd_b" },
          },
        ],
      },
    ]);
    expect(
      await screen.findByText("Publishing 1.4.1. Its build has started."),
    ).toBeDefined();
    expect(
      await screen.findByRole("heading", { level: 2, name: /1\.4\.1/ }),
    ).toBeDefined();
  });

  it("clears the acknowledgement once a warning joins the report", async () => {
    let findings = [warning()];
    const calls = releasesPage({ validation: () => report(findings) });
    const { queryClient } = await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const box = within(dialog).getByRole("checkbox") as HTMLInputElement;
    await user.click(box);
    expect(box.checked).toBe(true);

    findings = [
      warning(),
      warning({ entity_id: "nd_b", node_id: "nd_b", node_title: "Reply" }),
    ];
    await act(() =>
      queryClient.invalidateQueries({
        queryKey: validationKey(testProject.id),
      }),
    );
    const again = await within(dialog).findByRole("checkbox", {
      name: "I have read these 2 warnings and want to publish with them",
    });
    expect((again as HTMLInputElement).checked).toBe(false);
    expect(
      within(dialog).getByText(/The warnings changed after you ticked this/),
    ).toBeDefined();
    expect(
      (
        within(dialog).getByRole("button", {
          name: "Publish",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(callsOf(calls, "publishSnapshot")).toHaveLength(0);
  });

  it("lists every warning that its box acknowledges", async () => {
    const many = Array.from({ length: 25 }, (_, i) =>
      warning({
        entity_id: `nd_${i}`,
        node_id: `nd_${i}`,
        message: `Warning number ${i}.`,
      }),
    );
    releasesPage({ validation: () => report(many) });
    await openReleases();
    const dialog = await openDialog(userEvent.setup());
    for (const finding of many) {
      expect(within(dialog).getByText(finding.message)).toBeDefined();
    }
    expect(
      within(dialog).getByRole("checkbox", {
        name: "I have read these 25 warnings and want to publish with them",
      }),
    ).toBeDefined();
  });

  it("waits for the page's batches before it publishes", async () => {
    const calls = releasesPage();
    const { documents } = await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const version = within(dialog).getByRole("textbox", { name: /Version/ });
    await waitFor(() =>
      expect((version as HTMLInputElement).value).toBe("1.4.1"),
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: /Changelog note/ }),
      "A note",
    );
    const doc = documents.get(testProject.id);
    act(() => doc.store.setState({ sending: 1 }));
    const submit = within(dialog).getByRole("button", {
      name: "Publish",
    }) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    expect(within(dialog).getByText(/still being saved/)).toBeDefined();
    fireEvent.submit(submit.closest("form") as HTMLFormElement);
    expect(callsOf(calls, "publishSnapshot")).toHaveLength(0);
    act(() => doc.store.setState({ sending: 0 }));
    expect(submit.disabled).toBe(false);
    expect(within(dialog).queryByText(/still being saved/)).toBeNull();
  });

  it("does not call a project with releases new where the proposed version could not be read", async () => {
    releasesPage({
      next: () => {
        throw refused(500, { code: "internal" });
      },
    });
    await openReleases();
    const dialog = await openDialog(userEvent.setup());
    expect(
      await within(dialog).findByText(/proposed version could not be read/),
    ).toBeDefined();
    expect(within(dialog).queryByText(/first release/)).toBeNull();
  });

  it("suggests a major version where the diff removes content, and takes it", async () => {
    releasesPage({
      diff: () => diffOf({ summary: { ...diffOf().summary, removed: 2 } }),
    });
    await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    expect(
      await within(dialog).findByText(
        "This draft removes content or retypes a variable. Consider the major version 2.0.0.",
      ),
    ).toBeDefined();
    const version = within(dialog).getByRole("textbox", {
      name: /Version/,
    }) as HTMLInputElement;
    await waitFor(() => expect(version.value).toBe("1.4.1"));
    await user.click(within(dialog).getByRole("button", { name: "Use 2.0.0" }));
    expect(version.value).toBe("2.0.0");
    // A major version is what it suggested, so it says no more.
    expect(
      within(dialog).queryByRole("button", { name: "Use 2.0.0" }),
    ).toBeNull();
  });

  it("suggests a major version where a variable is retyped", async () => {
    releasesPage({
      diff: () =>
        diffOf({
          project: {
            ...diffOf().project,
            variables: [
              {
                id: "var_a",
                kind: "variable",
                change: "modified",
                key: "gold",
                fields: [{ field: "type", before: "int", after: "string" }],
              },
            ],
          },
          summary: {
            ...diffOf().summary,
            modified: 1,
            kinds: {
              ...diffOf().summary.kinds,
              variable: { ...noCounts, modified: 1 },
            },
          },
        }),
    });
    await openReleases();
    const dialog = await openDialog(userEvent.setup());
    expect(
      await within(dialog).findByText(
        "This draft removes content or retypes a variable. Consider the major version 2.0.0.",
      ),
    ).toBeDefined();
  });

  it("suggests nothing of a diff that neither removes nor retypes", async () => {
    const calls = releasesPage();
    await openReleases();
    const dialog = await openDialog(userEvent.setup());
    await waitFor(() =>
      expect(callsOf(calls, "diffSnapshots").length).toBeGreaterThan(0),
    );
    expect(within(dialog).queryByRole("button", { name: /^Use / })).toBeNull();
  });

  it("reads the diff that it suggests from once the page's batches are answered", async () => {
    const calls = releasesPage({
      diff: () => diffOf({ summary: { ...diffOf().summary, removed: 1 } }),
    });
    const { documents } = await openReleases();
    const doc = documents.get(testProject.id);
    act(() => doc.store.setState({ sending: 1 }));
    const dialog = await openDialog(userEvent.setup());
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(0);
    act(() => doc.store.setState({ sending: 0 }));
    expect(
      await within(dialog).findByText(/Consider the major version 2\.0\.0/),
    ).toBeDefined();
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(1);
  });

  it("says nothing of a failed comparison once the version is of a greater major", async () => {
    const calls = releasesPage({
      diff: () => {
        throw refused(500, { code: "internal" });
      },
    });
    await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const notice = /could not be compared/;
    expect(await within(dialog).findByText(notice)).toBeDefined();
    // The route counts in the rate limit of writes, so it is sent once.
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(1);
    const version = within(dialog).getByRole("textbox", { name: /Version/ });
    await waitFor(() =>
      expect((version as HTMLInputElement).value).toBe("1.4.1"),
    );
    await user.clear(version);
    await user.type(version, "2.0.0");
    expect(within(dialog).queryByText(notice)).toBeNull();
  });

  it("shows a version that is not newer at its field", async () => {
    releasesPage({
      publish: () => {
        throw refused(409, notNewerBody);
      },
    });
    await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const version = within(dialog).getByRole("textbox", { name: /Version/ });
    await waitFor(() =>
      expect((version as HTMLInputElement).value).toBe("1.4.1"),
    );
    await user.type(
      within(dialog).getByRole("textbox", { name: /Changelog note/ }),
      "A note",
    );
    await user.click(within(dialog).getByRole("button", { name: "Publish" }));
    await waitFor(() =>
      expect(version.getAttribute("aria-invalid")).toBe("true"),
    );
    expect(document.activeElement).toBe(version);
    expect(screen.getByRole("dialog")).toBeDefined();
  });

  it("keeps a refused version in its field as a newer proposal arrives", async () => {
    let proposed = "1.4.1";
    const calls = releasesPage({
      next: () => ({ version: proposed, latest_version: "1.4.0" }),
      publish: () => {
        proposed = "1.4.2";
        throw refused(409, notNewerBody);
      },
    });
    await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const version = within(dialog).getByRole("textbox", {
      name: /Version/,
    }) as HTMLInputElement;
    await waitFor(() => expect(version.value).toBe("1.4.1"));
    await user.type(
      within(dialog).getByRole("textbox", { name: /Changelog note/ }),
      "A note",
    );
    await user.click(within(dialog).getByRole("button", { name: "Publish" }));
    await waitFor(() =>
      expect(version.getAttribute("aria-invalid")).toBe("true"),
    );
    await waitFor(() =>
      expect(callsOf(calls, "getNextSnapshotVersion").length).toBeGreaterThan(
        1,
      ),
    );
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(version.value).toBe("1.4.1");
    expect(version.getAttribute("aria-invalid")).toBe("true");
    expect(within(dialog).getByText(/is not greater/i)).toBeDefined();
    await user.clear(version);
    await user.type(version, "1.4.2");
    expect(version.getAttribute("aria-invalid")).not.toBe("true");
    expect(within(dialog).queryByText(/is not greater/i)).toBeNull();
  });

  it("says that the report may be out of date where reading it again failed", async () => {
    const blocking = warning({ code: "jumper_broken", severity: "error" });
    let fails = false;
    releasesPage({
      validation: () => {
        if (fails) {
          throw refused(429, { code: "rate_limited" });
        }
        return report([blocking]);
      },
    });
    const { queryClient } = await openReleases();
    const dialog = await openDialog(userEvent.setup());
    expect(within(dialog).queryByText(/may be out of date/)).toBeNull();
    fails = true;
    await act(() =>
      queryClient.invalidateQueries({
        queryKey: validationKey(testProject.id),
      }),
    );
    expect(await within(dialog).findByText(/may be out of date/)).toBeDefined();
    expect(within(dialog).getByText(blocking.message)).toBeDefined();
    fails = false;
    await act(() =>
      queryClient.invalidateQueries({
        queryKey: validationKey(testProject.id),
      }),
    );
    await waitFor(() =>
      expect(within(dialog).queryByText(/may be out of date/)).toBeNull(),
    );
  });

  it("drops a refusal of the report once a newer report has come", async () => {
    let current = report([]);
    releasesPage({
      validation: () => current,
      publish: () => {
        throw refused(409, {
          code: "publish_blocked",
          message: "The draft has errors.",
        });
      },
    });
    const { queryClient } = await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const version = within(dialog).getByRole("textbox", {
      name: /Version/,
    }) as HTMLInputElement;
    await waitFor(() => expect(version.value).toBe("1.4.1"));
    await user.type(
      within(dialog).getByRole("textbox", { name: /Changelog note/ }),
      "A note",
    );
    await user.click(within(dialog).getByRole("button", { name: "Publish" }));
    const alerts = () =>
      within(dialog)
        .getAllByRole("alert")
        .map((alert) => alert.textContent)
        .join("");
    await waitFor(() => expect(alerts()).not.toBe(""));
    current = report([], 8);
    await act(() =>
      queryClient.invalidateQueries({
        queryKey: validationKey(testProject.id),
      }),
    );
    await waitFor(() => expect(alerts()).toBe(""));
  });

  it("drops its complaint of an empty version once the proposal fills the field", async () => {
    let answer: (proposal: unknown) => void = () => {};
    const proposal = new Promise((resolve) => {
      answer = resolve;
    });
    releasesPage({ next: () => proposal });
    await openReleases();
    const user = userEvent.setup();
    const dialog = await openDialog(user);
    const version = within(dialog).getByRole("textbox", {
      name: /Version/,
    }) as HTMLInputElement;
    await user.type(
      within(dialog).getByRole("textbox", { name: /Changelog note/ }),
      "A note",
    );
    await user.click(within(dialog).getByRole("button", { name: "Publish" }));
    await waitFor(() =>
      expect(version.getAttribute("aria-invalid")).toBe("true"),
    );
    await act(async () => {
      answer({ version: "1.4.1", latest_version: "1.4.0" });
    });
    await waitFor(() => expect(version.value).toBe("1.4.1"));
    expect(version.getAttribute("aria-invalid")).not.toBe("true");
  });

  it("compares with the newest ready release rather than the one that it kept", async () => {
    let list = [older];
    const calls = releasesPage({ list: () => list });
    const { queryClient } = await openReleases();
    const user = userEvent.setup();
    const first = await openDialog(user);
    await waitFor(() =>
      expect(callsOf(calls, "diffSnapshots")).toHaveLength(1),
    );
    await user.click(within(first).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    list = [newer, older];
    await act(() => readReleasesAgain(queryClient, testProject.id));
    await openDialog(user);
    await waitFor(() =>
      expect(callsOf(calls, "diffSnapshots").length).toBeGreaterThan(1),
    );
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    const sent = callsOf(calls, "diffSnapshots").map(
      ([, input]) => (input as { body: { from: string } }).body.from,
    );
    expect(sent).toEqual([olderId, newerId]);
  });
});

describe("a release's read-only view", () => {
  // Verifies: FR-SNAP-02/AC2
  it("shows what the release holds, and cannot emit a command", async () => {
    const calls = releasesPage({ role: "owner" });
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Release 1.4.0" });
    await screen.findByText("Welcome to the harbour.");
    expect(screen.getByText("Spoken by Elara")).toBeDefined();
    expect(screen.getByText("gold > 0")).toBeDefined();
    expect(screen.getByText("Not reached from the entry node")).toBeDefined();
    expect(screen.getByText("gold")).toBeDefined();
    expect(screen.getByText("Elara")).toBeDefined();

    // Nothing on the page edits: no field, and neither the undo nor the
    // redo that the project's other pages show an editor.
    expect(screen.queryAllByRole("textbox")).toEqual([]);
    expect(screen.queryByRole("button", { name: /^Undo/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Redo/ })).toBeNull();

    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "Tower" }));
    await screen.findByText("The wind howls.");
    await user.keyboard("{Control>}z{/Control}{Control>}y{/Control}");
    await user.click(screen.getByRole("link", { name: "Harbour" }));
    await user.click(
      await screen.findByRole("link", { name: "Jumps to Summit on Tower" }),
    );
    await screen.findByText("The wind howls.");
    expect(callsOf(calls, "applyCommands")).toEqual([]);
    expect(calls.mock.calls.map(([op]) => op)).not.toContain("applyCommands");
  });

  it("reads the release's files once, at the version that it reads", async () => {
    const calls = releasesPage();
    const { router } = renderApp(
      `/projects/${testProject.id}/releases/${newerId}?board=${towerId}`,
      { session: testSession },
    );
    await screen.findByText("The wind howls.");
    await act(() =>
      router.navigate({
        to: "/projects/$projectId/releases/$snapshotId",
        params: { projectId: testProject.id, snapshotId: newerId },
        search: { board: harbourId },
      }),
    );
    await screen.findByText("Welcome to the harbour.");
    const exports = callsOf(calls, "exportSnapshot");
    expect(exports).toHaveLength(1);
    expect(exports[0][1]).toEqual({
      path: { project_id: testProject.id, snapshot_id: newerId },
      query: { schema: 1 },
    });
  });

  it("opens the board of a jumper's target", async () => {
    releasesPage();
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    const user = userEvent.setup();
    await user.click(
      await screen.findByRole("link", { name: "Jumps to Summit on Tower" }),
    );
    await screen.findByText("The wind howls.");
    expect(
      screen.getByRole("link", { name: "Tower" }).getAttribute("aria-current"),
    ).toBe("page");
  });

  it("names a jumper's target on its own board, with no link", async () => {
    const files = filesWith(
      "boards/harbour.json",
      (file: { nodes: { id: string; target?: unknown }[] }) => {
        const jumper = file.nodes.find((n) => n.id === "nd_e");
        if (jumper !== undefined) {
          jumper.target = { boardId: harbourId, nodeId: "nd_b" };
        }
      },
    );
    releasesPage({ zip: await zipOf(files) });
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    expect(await screen.findByText("Jumps to Reply on Harbour")).toBeDefined();
    expect(
      screen.queryByRole("link", { name: "Jumps to Reply on Harbour" }),
    ).toBeNull();
  });

  it("calls no node unreached on a board without an entry node", async () => {
    const files = filesWith(
      "boards/tower.json",
      (file: { board: { entryNodeId?: string } }) => {
        delete file.board.entryNodeId;
      },
    );
    releasesPage({ zip: await zipOf(files) });
    renderApp(
      `/projects/${testProject.id}/releases/${newerId}?board=${towerId}`,
      { session: testSession },
    );
    await screen.findByText("The wind howls.");
    expect(screen.queryByText("Not reached from the entry node")).toBeNull();
  });

  it("keeps the stored report on the page where reading it again fails", async () => {
    let fails = false;
    releasesPage({
      stored: () => {
        if (fails) {
          throw refused(429, { code: "rate_limited" });
        }
        return report([warning()]);
      },
    });
    const { queryClient } = renderApp(
      `/projects/${testProject.id}/releases/${newerId}`,
      { session: testSession },
    );
    await screen.findByText("Nothing leaves this node.");
    fails = true;
    const { queryKey } = releaseReportQuery(testProject.id, newerId);
    await act(() => queryClient.invalidateQueries({ queryKey }));
    expect(queryClient.getQueryState(queryKey)?.status).toBe("error");
    expect(screen.getByText("Nothing leaves this node.")).toBeDefined();
  });

  it("shows the blank line of a text that begins with an empty paragraph", async () => {
    const files = filesWith(
      "boards/tower.json",
      (file: { nodes: { text?: { segments: unknown[] } }[] }) => {
        file.nodes[0].text?.segments.unshift({ kind: "paragraph" });
      },
    );
    releasesPage({ zip: await zipOf(files) });
    renderApp(
      `/projects/${testProject.id}/releases/${newerId}?board=${towerId}`,
      { session: testSession },
    );
    const line = await screen.findByText("The wind howls.");
    const paragraph = line.closest("p") as HTMLElement;
    const before = paragraph.previousElementSibling;
    expect(before?.tagName).toBe("P");
    expect(before?.querySelector("br")).not.toBeNull();
    expect(before?.textContent).toBe("");
  });

  it("spends no export on a read that failed as the window gets the focus again", async () => {
    const calls = releasesPage({ zip: new Uint8Array(8) });
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    await screen.findByText(
      "This release could not be read here. Download its zip instead.",
    );
    try {
      await act(async () => {
        focusManager.setFocused(false);
        focusManager.setFocused(true);
        await new Promise((resolve) => setTimeout(resolve, 20));
      });
    } finally {
      focusManager.setFocused(undefined);
    }
    expect(callsOf(calls, "exportSnapshot")).toHaveLength(1);
  });

  it("spends one export on a read whose answer was cut off", async () => {
    const calls = releasesPage();
    answerExport(calls, () =>
      Promise.reject(new NetworkError(new TypeError("body stream ended"))),
    );
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    await screen.findByRole("heading", { level: 1, name: "Release 1.4.0" });
    await waitFor(() =>
      expect(callsOf(calls, "exportSnapshot")).toHaveLength(1),
    );
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(callsOf(calls, "exportSnapshot")).toHaveLength(1);
  });

  it("spends no export on its files as the project is read again", async () => {
    const calls = releasesPage();
    const { queryClient } = renderApp(
      `/projects/${testProject.id}/releases/${newerId}`,
      { session: testSession },
    );
    await screen.findByText("Welcome to the harbour.");
    await act(() => readProjectAgain(queryClient, testProject.id));
    expect(callsOf(calls, "exportSnapshot")).toHaveLength(1);
  });

  it("lists the stored report by severity, with what is muted apart", async () => {
    const stored = report([
      warning({
        code: "expr_invalid",
        severity: "error",
        message: "The condition does not compile.",
      }),
      warning({ entity_id: "nd_b", node_id: "nd_b" }),
    ]);
    releasesPage({
      stored: () => ({
        ...stored,
        muted: [
          warning({
            entity_id: "nd_c",
            node_id: "nd_c",
            message: "A muted warning.",
            mute: { reason: "Fine.", muted_at: "2026-11-14T10:00:00Z" },
          }),
        ],
      }),
    });
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    await screen.findByRole("heading", { name: "Error (1)" });
    expect(screen.getByRole("heading", { name: "Warning (1)" })).toBeDefined();
    expect(screen.getByRole("heading", { name: "Muted (1)" })).toBeDefined();
    expect(screen.getByText("Reason: Fine.")).toBeDefined();
  });

  it("says why a release that it cannot read is not shown", async () => {
    const calls = releasesPage({ zip: new Uint8Array(8) });
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    expect(
      await screen.findByText(
        "This release could not be read here. Download its zip instead.",
      ),
    ).toBeDefined();
    // Each export counts against the hourly limit, and the same zip would
    // read the same again.
    expect(callsOf(calls, "exportSnapshot")).toHaveLength(1);
    expect(
      screen.getByRole("button", { name: "Download 1.4.0 as a zip" }),
    ).toBeDefined();
  });

  it("shows a failed release's reason and stored report, without files", async () => {
    const failed = snapshot({
      id: newerId,
      version: "1.4.0",
      status: "failed",
      failure_code: "validation_failed",
      counts: undefined,
    });
    const calls = releasesPage({ list: () => [failed] });
    renderApp(`/projects/${testProject.id}/releases/${newerId}`, {
      session: testSession,
    });
    await screen.findByText(/found validation errors/);
    expect(
      await screen.findByRole("heading", { name: "Validation report" }),
    ).toBeDefined();
    expect(screen.getByText("Nothing leaves this node.")).toBeDefined();
    expect(callsOf(calls, "exportSnapshot")).toEqual([]);
    expect(screen.queryByRole("button", { name: /Download/ })).toBeNull();
  });
});

describe("comparing releases", () => {
  // Verifies: FR-SNAP-04/AC4
  it("shows the summary, each change, a text's words, and collapses a change of position", async () => {
    const calls = releasesPage({
      diff: () =>
        diffOf({
          truncated: true,
          summary: {
            added: 12,
            cosmetic: 1,
            modified: 3,
            moved: 0,
            removed: 0,
            kinds: {
              ...diffOf().summary.kinds,
              node: { ...noCounts, added: 12, modified: 3, cosmetic: 1 },
            },
          },
          boards: [
            {
              board: { id: harbourId, name: "Harbour", slug: "harbour" },
              omitted: 4,
              changes: [
                {
                  id: "nd_a",
                  kind: "node",
                  change: "modified",
                  type: "element",
                  title: "Greet",
                  fields: [
                    {
                      field: "text",
                      before: "Hello there",
                      after: "Hello world",
                      words: [
                        { op: "equal", text: "Hello " },
                        { op: "delete", text: "there" },
                        { op: "insert", text: "world" },
                      ],
                    },
                    {
                      field: "condition",
                      before: "gold > 1",
                      after: "gold > 2",
                      refsChanged: true,
                    },
                  ],
                },
                {
                  id: "nd_b",
                  kind: "node",
                  change: "modified",
                  cosmetic: true,
                  type: "element",
                  title: "Reply",
                  fields: [
                    {
                      field: "position",
                      before: { x: 0, y: 0 },
                      after: { x: 1, y: 0 },
                    },
                  ],
                },
              ],
            },
          ],
        }),
    });
    renderApp(
      `/projects/${testProject.id}/releases/compare?from=${olderId}&to=${newerId}`,
      { session: testSession },
    );
    await screen.findByRole("heading", { level: 1, name: "Compare releases" });
    const row = (await screen.findByRole("rowheader", { name: "Nodes" }))
      .parentElement as HTMLElement;
    expect(
      within(row)
        .getAllByRole("cell")
        .map((c) => c.textContent),
    ).toEqual(["12", "3", "0", "0", "1"]);
    expect(screen.getByText(/too large to list whole/)).toBeDefined();
    expect(screen.getByText("4 more changes are not listed.")).toBeDefined();
    expect(screen.getByText("there").tagName).toBe("DEL");
    expect(screen.getByText("world").tagName).toBe("INS");
    expect(screen.getByText("Before: gold > 1")).toBeDefined();
    expect(
      screen.getByText("What this expression refers to changed."),
    ).toBeDefined();
    // A change of position alone is collapsed.
    const collapsed = screen
      .getByText("1 change of position only")
      .closest("details");
    expect(collapsed?.open).toBe(false);
    expect(collapsed?.textContent).toContain("Reply");
    expect(callsOf(calls, "diffSnapshots")[0][1]).toMatchObject({
      body: { from: olderId, to: newerId },
      headers: { "X-CSRF-Token": testSession.csrf_token },
    });
  });

  it("compares the newest two by default, and with the draft once asked", async () => {
    const calls = releasesPage();
    renderApp(`/projects/${testProject.id}/releases/compare`, {
      session: testSession,
    });
    await screen.findByText("These two are the same.");
    expect(callsOf(calls, "diffSnapshots")[0][1]).toMatchObject({
      body: { from: olderId, to: newerId },
    });
    const user = userEvent.setup();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "To" }),
      "draft",
    );
    await waitFor(() =>
      expect(
        callsOf(calls, "diffSnapshots").some(
          ([, input]) =>
            (input as { body: { to: string } }).body.to === "draft",
        ),
      ).toBe(true),
    );
  });

  it("holds the pair that it picked as a newer release becomes ready", async () => {
    let list = [newer, older];
    const calls = releasesPage({ list: () => list });
    const { router, queryClient } = renderApp(
      `/projects/${testProject.id}/releases/compare`,
      { session: testSession },
    );
    await screen.findByText("These two are the same.");
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        from: olderId,
        to: newerId,
      }),
    );
    list = [
      snapshot({ id: newId("snapshot"), version: "1.5.0" }),
      newer,
      older,
    ];
    await act(() => readReleasesAgain(queryClient, testProject.id));
    await waitFor(() => expect(screen.getAllByText("1.5.0")).toHaveLength(2));
    expect(
      (screen.getByRole("combobox", { name: "From" }) as HTMLSelectElement)
        .value,
    ).toBe(olderId);
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(1);
  });

  it("shows no diff of the pair before while it reads the next", async () => {
    let answer: () => Diff | Promise<Diff> = () => diffOf({ truncated: true });
    let finish: (diff: Diff) => void = () => undefined;
    releasesPage({ diff: () => answer() });
    renderApp(
      `/projects/${testProject.id}/releases/compare?from=${olderId}&to=${newerId}`,
      { session: testSession },
    );
    await screen.findByText(/too large to list whole/);
    answer = () =>
      new Promise<Diff>((resolve) => {
        finish = resolve;
      });
    const user = userEvent.setup();
    await user.selectOptions(
      screen.getByRole("combobox", { name: "To" }),
      "draft",
    );
    expect(await screen.findByText("Comparing…")).toBeDefined();
    expect(screen.queryByText(/too large to list whole/)).toBeNull();
    await act(async () => finish(diffOf()));
    expect(await screen.findByText("These two are the same.")).toBeDefined();
  });

  it("reads a comparison with the draft once, and again when asked", async () => {
    const realtime = fakeRealtime();
    const calls = releasesPage();
    renderApp(
      `/projects/${testProject.id}/releases/compare?from=${newerId}&to=draft`,
      { session: testSession, realtime },
    );
    await screen.findByText("These two are the same.");
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(1);
    act(() =>
      realtime.send(
        testProject.id,
        [{ type: "node.update", payload: { id: "nd_a", title: "Greet" } }],
        [{ id: "nd_a", rev: 2 }],
      ),
    );
    await act(() => new Promise((resolve) => setTimeout(resolve, 20)));
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(1);
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Compare again" }));
    await waitFor(() =>
      expect(callsOf(calls, "diffSnapshots")).toHaveLength(2),
    );
  });

  it("offers no new comparison of two releases, which never change", async () => {
    releasesPage();
    renderApp(
      `/projects/${testProject.id}/releases/compare?from=${olderId}&to=${newerId}`,
      { session: testSession },
    );
    await screen.findByText("These two are the same.");
    expect(screen.queryByRole("button", { name: "Compare again" })).toBeNull();
  });

  it("offers a failed comparison of two releases again", async () => {
    let fails = true;
    const calls = releasesPage({
      diff: () => {
        if (fails) {
          throw refused(500, { code: "internal" });
        }
        return diffOf();
      },
    });
    renderApp(
      `/projects/${testProject.id}/releases/compare?from=${olderId}&to=${newerId}`,
      { session: testSession },
    );
    const again = await screen.findByRole("button", { name: "Compare again" });
    // The route counts in the rate limit of writes, so it is sent once.
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(1);
    fails = false;
    await userEvent.setup().click(again);
    await screen.findByText("These two are the same.");
    expect(callsOf(calls, "diffSnapshots")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: "Compare again" })).toBeNull();
  });

  it("names a side that no ready release is as what it is", async () => {
    const gone = newId("snapshot");
    releasesPage();
    renderApp(
      `/projects/${testProject.id}/releases/compare?from=${gone}&to=${newerId}`,
      { session: testSession },
    );
    const from = (await screen.findByRole("combobox", {
      name: "From",
    })) as HTMLSelectElement;
    expect(from.value).toBe(gone);
    expect(from.selectedOptions[0].textContent).toBe(
      "A release not listed here",
    );
  });

  it("says so where a text's words are the same, without saying what else changed", async () => {
    releasesPage({
      diff: () =>
        diffOf({
          summary: {
            ...diffOf().summary,
            modified: 1,
            kinds: {
              ...diffOf().summary.kinds,
              node: { ...noCounts, modified: 1 },
            },
          },
          boards: [
            {
              board: { id: harbourId, name: "Harbour", slug: "harbour" },
              omitted: 0,
              changes: [
                {
                  id: "nd_a",
                  kind: "node",
                  change: "modified",
                  type: "element",
                  title: "Greet",
                  fields: [
                    {
                      field: "text",
                      before: "Hello",
                      after: "Hello",
                      words: [{ op: "equal", text: "Hello" }],
                    },
                  ],
                },
              ],
            },
          ],
        }),
    });
    renderApp(
      `/projects/${testProject.id}/releases/compare?from=${olderId}&to=${newerId}`,
      { session: testSession },
    );
    expect(
      await screen.findByText(
        "Its words are the same. Its formatting, links, or what it refers to changed.",
      ),
    ).toBeDefined();
  });

  it("says so where there is nothing to compare", async () => {
    releasesPage({ list: () => [] });
    renderApp(`/projects/${testProject.id}/releases/compare`, {
      session: testSession,
    });
    expect(
      await screen.findByText("There is no ready release to compare yet."),
    ).toBeDefined();
  });
});
