import { expect, test } from "@playwright/test";

test("one to one call shows a remote tile", async ({ browser }) => {
  test.skip(!process.env.E2E, "set E2E=1 when the stack is running");
  const left = await browser.newPage();
  const right = await browser.newPage();
  const stamp = Date.now();
  await register(left, `a-${stamp}@example.com`, `a${stamp % 10000}`);
  await register(right, `b-${stamp}@example.com`, `b${stamp % 10000}`);
  await left.getByTitle("Друзья").click();
  await left.getByPlaceholder("Имя#0000").fill(`b${stamp % 10000}`);
  await left.getByRole("button", { name: "Добавить" }).click();
  await right.getByTitle("Друзья").click();
  await right.getByRole("button", { name: "Принять" }).click();
  await left.getByText(`b${stamp % 10000}`).click();
  await left.getByRole("button").filter({ has: left.locator("svg") }).nth(1).click();
  await expect(right.getByRole("dialog")).toBeVisible();
  await right.getByRole("button", { name: "Принять" }).click();
  await expect(left.locator("audio, video").first()).toBeVisible({ timeout: 15000 });
  await right.getByRole("button", { name: "Завершить" }).click();
});

async function register(page: import("@playwright/test").Page, email: string, username: string) {
  await page.goto("/register/");
  await page.getByPlaceholder("Email").fill(email);
  await page.getByPlaceholder("Имя пользователя").fill(username);
  await page.getByPlaceholder("Пароль").fill("supersecret");
  await page.getByRole("button", { name: "Зарегистрироваться" }).click();
  await expect(page).toHaveURL(/\/app/);
}
