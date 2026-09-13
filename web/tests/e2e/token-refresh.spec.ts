import { test, expect } from "@playwright/test";
import { signInAndBrowse } from "./helpers";

type ShortSignIn = { __ta: { signInShort: (uid: string, expSeconds: number) => Promise<void> } };

test("expired token auto-refreshes on the next api call", async ({ page }) => {
  await signInAndBrowse(page, "admin-1");

  // Swap in a token that is already past its expiry, so the very next request
  // gets a 401 and must go through the refresh-and-retry path.
  await page.evaluate(
    () => (window as unknown as ShortSignIn).__ta.signInShort("admin-1", -60),
  );

  await page.getByRole("link", { name: "Albums" }).click();
  await expect(page.getByRole("heading", { name: "Albums" })).toBeVisible();
  await expect(page.locator("a[href^='/albums/']").first()).toBeVisible({ timeout: 15_000 });
  await expect(page).not.toHaveURL(/\/login/);
});
