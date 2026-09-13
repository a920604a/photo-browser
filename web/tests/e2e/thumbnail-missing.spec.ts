import { test, expect } from "@playwright/test";
import { signInAndBrowse } from "./helpers";

// Rather than deleting a file from the shared thumbnail volume (which would
// leak into every later run), fail one thumbnail request at the network layer.
test("grid stays usable when a thumbnail 404s", async ({ page }) => {
  let failed = 0;
  await page.route("**/api/v1/photos/*/thumbnail/*", async (route) => {
    if (failed === 0) {
      failed++;
      await route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "not_found", message: "thumbnail missing" } }),
      });
      return;
    }
    await route.continue();
  });

  await signInAndBrowse(page, "admin-1");

  const tiles = page.locator("a[href^='/viewer/']");
  await expect(tiles.first()).toBeVisible({ timeout: 15_000 });
  // The broken tile degrades to a placeholder; its neighbours still render.
  await expect(page.getByText("n/a").first()).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("a[href^='/viewer/'] img").first()).toHaveAttribute("src", /^blob:/);
});
