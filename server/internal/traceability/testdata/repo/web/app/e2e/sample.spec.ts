import { expect, test } from "@playwright/test";

test.describe("a playwright group", () => {
  // Verifies: FR-A-01/AC1
  test("a playwright test", async ({ page }) => {
    await expect(page).toHaveTitle("Storycreep");
  });
});
