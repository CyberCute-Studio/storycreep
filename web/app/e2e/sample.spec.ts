import { signedIn } from "./content.ts";
import { en, expect, press, test, violations } from "./fixtures.ts";

// The sample project and project duplication (PRD FR-PROJ-05, FR-PROJ-01,
// plan note N92): the empty list of a first sign-in offers the sample, which
// one click opens as a project of the workspace, whose validation has its
// one warning; and a project's settings duplicate it into a copy, which the
// page goes to.

// Verifies: FR-PROJ-05/AC1, NFR-A11Y-08
test("the sample opens in one click, and its settings duplicate it", async ({
  page,
}) => {
  await signedIn(page, "Sam Sample");
  await expect(page.getByText(en["projects.empty_offer"])).toBeVisible();
  expect(await violations(page)).toEqual([]);
  await press(page.getByRole("button", { name: en["sample_project.open"] }));
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    "The Lighthouse Keeper",
  );
  for (const board of [
    "Harbour Intro",
    "Elara — First Meeting",
    "Harbourmaster",
  ]) {
    await expect(page.getByText(board, { exact: true }).first()).toBeVisible();
  }

  await press(
    page.getByRole("link", { name: en["project.nav.validation"], exact: true }),
  );
  await expect(
    page.getByRole("heading", { level: 1, name: en["validation.title"] }),
  ).toBeVisible();
  await expect(page.getByText(/heard_rumour/).first()).toBeVisible();

  await press(
    page.getByRole("link", { name: en["project.nav.settings"], exact: true }),
  );
  await press(
    page.getByRole("button", { name: en["project_settings.duplicate"] }),
  );
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByLabel(en["project_settings.duplicate_name"], { exact: true }),
  ).toHaveValue("The Lighthouse Keeper (copy)");
  expect(await violations(page)).toEqual([]);
  await press(
    dialog.getByRole("button", {
      name: en["project_settings.duplicate_submit"],
    }),
  );
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(
    "The Lighthouse Keeper (copy)",
  );
  await expect(
    page.getByText("Harbour Intro", { exact: true }).first(),
  ).toBeVisible();
});
