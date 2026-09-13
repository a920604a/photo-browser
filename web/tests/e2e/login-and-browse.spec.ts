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
  await page.locator("a[href^='/albums/']").first().click();
  await expect(page).toHaveURL(/\/albums\/\d+/);
  await expect(page.locator("a[href^='/viewer/']").first()).toBeVisible({ timeout: 15_000 });

  await page.getByRole("link", { name: "Categories" }).click();
  await expect(page).toHaveURL(/\/categories/);
  await page.locator("a[href^='/categories/']").first().click();
  await expect(page.locator("a[href^='/albums/']").first()).toBeVisible();

  await page.getByRole("link", { name: "Photos" }).click();
  await page.getByRole("tab", { name: "Timeline" }).click();
  await expect(page).toHaveURL(/\/photos\/timeline/);
});

test("the layout fits a 360px viewport without horizontal scroll", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 640 });
  await signInAndBrowse(page, "member-1");
  await expect(page.locator("a[href^='/viewer/']").first()).toBeVisible({ timeout: 15_000 });

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);

  // Every bottom-nav target stays a comfortable tap size.
  const boxes = await page.locator("nav[aria-label='Primary'] a").evaluateAll((els) =>
    els.map((e) => {
      const r = e.getBoundingClientRect();
      return { w: r.width, h: r.height };
    }),
  );
  expect(boxes.length).toBe(4);
  for (const b of boxes) {
    expect(b.w).toBeGreaterThanOrEqual(44);
    expect(b.h).toBeGreaterThanOrEqual(44);
  }
});
