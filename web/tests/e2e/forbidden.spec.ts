import { test, expect } from "@playwright/test";
import { signInAs } from "./helpers";

// denied-1 is minted by testauth but deliberately absent from the compose
// stack's allowlist, so /me answers 403.
test("unallowlisted user lands on /forbidden", async ({ page }) => {
  await signInAs(page, "denied-1");
  await expect(page).toHaveURL(/\/forbidden/);
  await expect(page.getByText(/Access denied/)).toBeVisible();
});

test("logging out of /forbidden returns to the login screen", async ({ page }) => {
  await signInAs(page, "denied-1");
  await page.getByRole("button", { name: /Log out/ }).click();
  await expect(page).toHaveURL(/\/login/);
});
