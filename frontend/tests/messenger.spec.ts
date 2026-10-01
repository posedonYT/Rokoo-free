import { expect, test } from "@playwright/test";

const email = `rokko-${Date.now()}@example.com`;

test("register, login, create a server and send a message", async ({ page }) => {
  test.skip(!process.env.E2E, "set E2E=1 when envoy and the services are running");
  await page.goto("/register/");
  await page.getByPlaceholder("Email").fill(email);
  await page.getByPlaceholder("Имя пользователя").fill("rokko");
  await page.getByPlaceholder("Пароль").fill("supersecret");
  await page.getByRole("button", { name: "Зарегистрироваться" }).click();
  await expect(page).toHaveURL(/\/app/);

  await page.getByTitle("Создать сервер").click();
  page.once("dialog", (dialog) => dialog.accept("E2E"));
  await page.getByTitle("Создать сервер").click();
  await page.getByTitle("E2E").click();
  await page.getByRole("button", { name: "general", exact: true }).click();
  await page.getByPlaceholder("Сообщение").fill("привет");
  await page.getByRole("button", { name: "Отправить" }).click();
  await expect(page.getByText("привет")).toBeVisible();
});

test("login page is reachable", async ({ page }) => {
  await page.goto("/login/");
  await expect(page.getByRole("button", { name: "Войти" })).toBeVisible();
});
