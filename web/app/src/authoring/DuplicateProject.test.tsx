import { createRootRoute, createRoute } from "@tanstack/react-router";
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderApp, testSession } from "../test/app";
import { fakeApi, refused, testProject } from "../test/fake";
import { copyName, DuplicateProject } from "./DuplicateProject";

// Duplicating a project (PRD FR-PROJ-01): the dialog asks for the copy's
// name, the server makes the copy, and the page goes to it.

function render() {
  const root = createRootRoute();
  return renderApp("/settings", {
    session: testSession,
    routes: root.addChildren([
      createRoute({
        getParentRoute: () => root,
        path: "/settings",
        component: () => <DuplicateProject project={testProject} />,
      }),
      createRoute({
        getParentRoute: () => root,
        path: "/projects/$projectId",
        component: () => <p>The copy's page</p>,
      }),
    ]),
  });
}

async function openDialog() {
  await userEvent.click(
    await screen.findByRole("button", { name: "Duplicate the project" }),
  );
  return screen.findByRole("dialog", { name: "Duplicate Harbour" });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("duplicating a project", () => {
  // Verifies: FR-PROJ-01/AC1
  it("offers a name for the copy, makes it, and goes to it", async () => {
    const call = fakeApi((operation) => {
      switch (operation) {
        case "getWorkspaceUsage":
          return {};
        case "listProjects":
          return { items: [], next_cursor: null };
        case "duplicateProject":
          return { ...testProject, id: "prj_copy", name: "Harbour (copy)" };
      }
      throw new Error(`unexpected ${operation}`);
    });
    const { router } = render();
    const dialog = await openDialog();
    const name = within(dialog).getByLabelText("Name of the copy");
    expect(name).toHaveProperty("value", "Harbour (copy)");
    await userEvent.clear(name);
    await userEvent.type(name, "Harbour, take two");
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Duplicate" }),
    );
    await screen.findByText("The copy's page");
    expect(router.state.location.pathname).toBe("/projects/prj_copy");
    expect(call).toHaveBeenCalledWith(
      "duplicateProject",
      expect.objectContaining({
        path: { project_id: testProject.id },
        body: { name: "Harbour, take two" },
      }),
    );
  });

  it("shows a refusal of the name at its field, and one of the copy for the form", async () => {
    let answer = refused(422, {
      code: "validation_failed",
      message: "The request is invalid.",
      details: { errors: [{ location: "body.name", message: "is too long" }] },
    });
    fakeApi((operation) => {
      switch (operation) {
        case "getWorkspaceUsage":
          return {};
        case "duplicateProject":
          throw answer;
      }
      throw new Error(`unexpected ${operation}`);
    });
    render();
    const dialog = await openDialog();
    const submit = within(dialog).getByRole("button", { name: "Duplicate" });
    await userEvent.click(submit);
    const name = within(dialog).getByLabelText("Name of the copy");
    await vi.waitFor(() =>
      expect(name.getAttribute("aria-invalid")).toBe("true"),
    );

    answer = refused(409, {
      code: "duplicate_refused",
      message:
        "This project could not be duplicated, as the import of its copy refused what it holds, such as more images than an import takes, or an image whose file is missing. Nothing was created.",
    });
    await userEvent.click(submit);
    expect((await within(dialog).findByRole("alert")).textContent).toContain(
      "This project could not be duplicated",
    );
    expect(screen.queryByText("The copy's page")).toBeNull();
  });
});

describe("the name of a copy", () => {
  const format = (name: string) => `${name} (copy)`;

  it("is the catalog's, of the project's name", () => {
    expect(copyName(format, "Harbour")).toBe("Harbour (copy)");
  });

  it("keeps as much of a long name as a project's name may hold, counted in characters", () => {
    const long = "🌊".repeat(100);
    const got = copyName(format, long);
    expect(Array.from(got)).toHaveLength(100);
    expect(got.endsWith(" (copy)")).toBe(true);
  });

  it("leaves out the control characters that a new name may not hold", () => {
    // U+202E RIGHT-TO-LEFT OVERRIDE and U+2028 LINE SEPARATOR, which an
    // imported project's stored name may hold.
    const stored = `Har${String.fromCodePoint(0x202e)}bour${String.fromCodePoint(0x2028)}`;
    expect(copyName(format, stored)).toBe("Harbour (copy)");
  });

  it("is a name that the field takes, and so is one of as many characters typed", async () => {
    fakeApi((operation) => {
      if (operation === "getWorkspaceUsage") {
        return {};
      }
      throw new Error(`unexpected ${operation}`);
    });
    const stored = { ...testProject, name: "🌊".repeat(100) };
    const root = createRootRoute();
    renderApp("/settings", {
      session: testSession,
      routes: root.addChildren([
        createRoute({
          getParentRoute: () => root,
          path: "/settings",
          component: () => <DuplicateProject project={stored} />,
        }),
      ]),
    });
    await userEvent.click(
      await screen.findByRole("button", { name: "Duplicate the project" }),
    );
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByLabelText(
      "Name of the copy",
    ) as HTMLInputElement;
    expect(name.value).toBe(copyName(format, stored.name));
    expect(name.value.length).toBeLessThanOrEqual(name.maxLength);
    await userEvent.clear(name);
    await userEvent.type(name, "🌊".repeat(100));
    expect(Array.from(name.value)).toHaveLength(100);
  });
});
