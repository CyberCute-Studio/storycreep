import { expect, test } from "@playwright/test";

// Verifies: FR-A-01/AC1
test.describe("a playwright group", () => {
  test("a playwright test", async ({ page }) => {
    await expect(page).toHaveTitle("Storycreep");
  });
});
