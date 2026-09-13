import { test, expect } from "@playwright/test";
import { signInAndBrowse } from "./helpers";

test("admin can browse from grid to viewer and log out", async ({ page }) => {
  await signInAndBrowse(page, "admin-1");

  const firstTile = page.locator("a[href^='/viewer/']").first();
  await expect(firstTile).toBeVisible({ timeout: 15_000 });
  // Thumbnails are fetched with the bearer token and rendered from object URLs.
  await expect(firstTile.locator("img")).toHaveAttribute("src", /^blob:/);

  const href = await firstTile.getAttribute("href");
  await firstTile.click();
  await expect(page).toHaveURL(new RegExp(href!.replace("?", "\\?")));

  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(dialog.locator("img")).toHaveAttribute("src", /^blob:/, { timeout: 15_000 });

  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(/\/photos/);

  await page.getByRole("link", { name: "Profile" }).click();
  await expect(page.getByText("Role: admin")).toBeVisible();
  await page.getByRole("button", { name: /Log out/ }).click();
  await expect(page).toHaveURL(/\/login/);
});

test("the main routes are reachable from the bottom nav", async ({ page }) => {
  await signInAndBrowse(page, "member-1");

  await page.getByRole("link", { name: "Albums" }).click();
  await expect(page).toHaveURL(/\/albums/);
  await expect(page.locator("a[href^='/albums/']").first()).toBeVisible();

  await page.getByRole("link", { name: "Categories" }).click();
  await expect(page).toHaveURL(/\/categories/);
  await page.locator("a[href^='/categories/']").first().click();
  await expect(page.locator("a[href^='/albums/']").first()).toBeVisible();

  await page.getByRole("link", { name: "Photos" }).click();
  await page.getByRole("tab", { name: "Timeline" }).click();
  await expect(page).toHaveURL(/\/photos\/timeline/);
});
