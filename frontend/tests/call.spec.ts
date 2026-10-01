import { expect, test } from "@playwright/test";

test("one to one call shows a remote tile", async ({ browser }) => {
  test.skip(!process.env.E2E, "set E2E=1 when the stack is running");
  const left = await browser.newPage();
  const right = await browser.newPage();
  await left.context().grantPermissions(["microphone", "camera"]);
  await right.context().grantPermissions(["microphone", "camera"]);
  const stamp = Date.now();
  await register(left, `a-${stamp}@example.com`, `a${stamp % 10000}`);
  await register(right, `b-${stamp}@example.com`, `b${stamp % 10000}`);
  await left.getByTitle("Друзья").click();
  await left.getByPlaceholder("Найти username или username#0000").fill(`b${stamp % 10000}`);
  await left.getByRole("button", { name: "Найти" }).click();
  await expect(left.getByRole("button", { name: "Добавить" })).toBeVisible({ timeout: 10000 });
  await left.getByRole("button", { name: "Добавить" }).click();
  await right.reload();
  await expect(right).toHaveURL(/\/app/);
  await right.getByTitle("Друзья").click();
  await expect(right.getByRole("button", { name: "Принять" })).toBeVisible({ timeout: 15000 });
  await right.getByRole("button", { name: "Принять" }).click();
  await left.reload();
  await expect(left).toHaveURL(/\/app/);
  await left.getByTitle("Друзья").click();
  await expect(left.getByRole("button", { name: "Написать" })).toBeVisible({ timeout: 10000 });
  await left.getByRole("button", { name: "Написать" }).click();
  await left.locator("button:has(svg.lucide-phone)").first().click();
  await expect(right.getByRole("dialog", { name: "Входящий звонок" })).toBeVisible({ timeout: 20000 });
  await right.getByRole("button", { name: "Принять", exact: true }).click();
  await expect(left.getByRole("button", { name: "Завершить" })).toBeVisible({ timeout: 20000 });
  await expect(left.locator("audio, video").first()).toBeAttached({ timeout: 20000 });
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
