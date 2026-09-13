import type { Page } from "@playwright/test";

export async function signInAs(page: Page, uid: string) {
  await page.goto("/login");
  await page.selectOption("select", uid);
  await page.getByRole("button", { name: /^Sign in$/ }).click();
}

/** Signs in and waits for the photo grid to be the current route. */
export async function signInAndBrowse(page: Page, uid: string) {
  await signInAs(page, uid);
  await page.waitForURL(/\/photos/);
}
