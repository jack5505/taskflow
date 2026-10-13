import { expect, login, test } from "./fixtures.ts";

// ТЗ §7: разделы в шапке с подсветкой текущего, одинаковые вкладки проекта на доске, в бэклоге
// и в настройках; меню пользователя с профилем и выходом.
test("навигация: шапка, вкладки проекта, меню пользователя", async ({ page, api }) => {
  const key = await api.project("Навигация");

  await login(page);
  await page.goto(`/projects/${key}`);
  const nav = page.getByRole("navigation", { name: "Разделы", exact: true });
  await expect(nav.getByRole("link", { name: new RegExp(key) })).toHaveClass(/active/);

  const tabs = page.getByRole("navigation", { name: "Разделы проекта" });
  await expect(tabs.getByRole("link", { name: "Доска" })).toHaveAttribute("aria-current", "page");
  await tabs.getByRole("link", { name: "Бэклог" }).click();
  await expect(page).toHaveURL(new RegExp(`/projects/${key}/backlog$`));
  await expect(tabs.getByRole("link", { name: "Бэклог" })).toHaveAttribute("aria-current", "page");

  // Менеджер видит настройки; со страницы настроек — обратно на доску теми же вкладками.
  await tabs.getByRole("link", { name: "Настройки" }).click();
  await expect(page).toHaveURL(new RegExp(`/projects/${key}/settings`));
  await tabs.getByRole("link", { name: "Доска" }).click();
  await expect(page).toHaveURL(new RegExp(`/projects/${key}$`));

  await nav.getByRole("link", { name: "Отчёты" }).click();
  await expect(nav.getByRole("link", { name: "Отчёты" })).toHaveAttribute("aria-current", "page");
  await expect(nav.getByRole("link", { name: new RegExp(key) })).toHaveCount(0);

  // Меню пользователя: профиль, Escape закрывает, выход ведёт на вход.
  const menu = page.getByLabel(/Меню пользователя/);
  await menu.click();
  await expect(page.getByRole("menuitem", { name: "Профиль" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("menuitem", { name: "Профиль" })).toBeHidden();
  await menu.click();
  await page.getByRole("menuitem", { name: "Выйти" }).click();
  await expect(page).toHaveURL(/\/login/);
});
