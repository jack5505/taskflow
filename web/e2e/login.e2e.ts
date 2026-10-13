import { expect, login, test } from "./fixtures.ts";
import { LOGIN_EMAIL, LOGIN_LINK } from "./env.ts";

// ТЗ §7.1, §10.4: первый администратор создаётся CLI-командой (e2e/stack.sh) и входит по ссылке.
// Ссылка одноразовая: повтор после её использования упал бы на погашенной ссылке и скрыл
// настоящую причину — без повторов.
test.describe.configure({ retries: 0 });

test("вход по ссылке установки пароля", async ({ page }) => {
  expect(LOGIN_LINK, "E2E_LOGIN_LINK: ссылку печатает e2e/stack.sh up").not.toBe("");
  await page.goto(new URL(LOGIN_LINK).pathname + new URL(LOGIN_LINK).search);

  await expect(page.getByRole("heading", { name: "Установка пароля" })).toBeVisible();
  // Пароль короче 10 символов не принимается (§7.1).
  await page.getByLabel("Новый пароль").fill("short");
  await page.getByLabel("Повторите пароль").fill("short");
  await page.getByRole("button", { name: "Задать пароль" }).click();
  await expect(page.getByRole("alert")).toHaveText("Пароль должен быть не короче 10 символов");

  const password = "first-admin-password";
  await page.getByLabel("Новый пароль").fill(password);
  await page.getByLabel("Повторите пароль").fill(password);
  await page.getByRole("button", { name: "Задать пароль" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByText("Пароль задан. Войдите с новым паролем.")).toBeVisible();

  await login(page, LOGIN_EMAIL, password);
  // Имя — в меню пользователя в шапке.
  await expect(page.getByRole("banner").getByLabel("Меню пользователя: Первый администратор")).toBeVisible();

  // Ссылка одноразовая: повторная установка пароля отклоняется.
  await page.goto(new URL(LOGIN_LINK).pathname + new URL(LOGIN_LINK).search);
  await page.getByLabel("Новый пароль").fill(password);
  await page.getByLabel("Повторите пароль").fill(password);
  await page.getByRole("button", { name: "Задать пароль" }).click();
  await expect(page.getByText("Ссылка недействительна или уже использована. Запросите новую.")).toBeVisible();
});
