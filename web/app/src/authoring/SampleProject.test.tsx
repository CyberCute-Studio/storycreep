import { useInfiniteQuery } from "@tanstack/react-query";
import { createRootRoute, createRoute } from "@tanstack/react-router";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { fakeApi, refused, testProject } from "../test/fake";
import { projectsQuery } from "../workspace/queries";
import { SampleProjectButton } from "./SampleProject";

// The one-click sample project (PRD FR-PROJ-05): the button imports it on
// the server, and goes to the new project.

/** The workspace's page, as far as the button is concerned. */
function WorkspacePage() {
  useInfiniteQuery(projectsQuery("ws_a"));
  return (
    <div>
      <SampleProjectButton workspaceId="ws_a" />
    </div>
  );
}

function render() {
  const root = createRootRoute();
  return renderApp("/workspace", {
    session: testSession,
    routes: root.addChildren([
      createRoute({
        getParentRoute: () => root,
        path: "/workspace",
        component: WorkspacePage,
      }),
      createRoute({
        getParentRoute: () => root,
        path: "/projects/$projectId",
        component: () => <p>The project's page</p>,
      }),
    ]),
  });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("the sample project's button", () => {
  // Verifies: FR-PROJ-05/AC1
  it("opens the sample on the server, goes to it, and reads the workspace's list again", async () => {
    const sample = {
      ...testProject,
      id: "prj_sample",
      name: "The Lighthouse Keeper",
    };
    let lists = 0;
    const call = fakeApi((operation) => {
      switch (operation) {
        case "listProjects":
          lists++;
          return { items: [], next_cursor: null };
        case "getWorkspaceUsage":
          return {};
        case "importSampleProject":
          return sample;
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = render();
    await userEvent.click(
      await screen.findByRole("button", { name: "Open the sample project" }),
    );
    await screen.findByText("The project's page");
    expect(router.state.location.pathname).toBe("/projects/prj_sample");
    expect(call).toHaveBeenCalledWith(
      "importSampleProject",
      expect.objectContaining({
        path: { workspace_id: "ws_a" },
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-token" }),
      }),
    );
    await waitFor(() => expect(lists).toBeGreaterThan(1));
  });

  it("says why the server refused, and stays on the page", async () => {
    fakeApi((operation) => {
      switch (operation) {
        case "listProjects":
          return { items: [], next_cursor: null };
        case "importSampleProject":
          throw refused(409, {
            code: "limit_exceeded",
            details: { key: "projects", usage: 3, limit: 3 },
          });
      }
      throw new Error(`unexpected ${operation}`);
    });
    render();
    const button = await screen.findByRole("button", {
      name: "Open the sample project",
    });
    await userEvent.click(button);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("3 of 3");
    expect(button).toHaveProperty("disabled", false);
  });
});
