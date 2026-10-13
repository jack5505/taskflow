import { expect, login, test } from "./fixtures.ts";

// ТЗ §7, #182: основные сценарии доски, карточки и создания задачи проходятся с клавиатуры.
test("клавиатура: окно держит фокус, вкладки стрелками, карточка и меню статуса", async ({ page, api }) => {
  const key = await api.project("Клавиатура");
  const sprint = await api.sprint(key, "Спринт клавиатуры");
  const t = await api.task(key, { title: "Пройти с клавиатуры", sprint_id: sprint.id, assignee_id: api.userId });
  await api.startSprint(sprint.id);

  await login(page);
  await page.goto(`/projects/${key}`);

  // Окно создания: Tab не выходит за окно, Escape закрывает и возвращает фокус на кнопку.
  const create = page.getByRole("button", { name: "Создать задачу" });
  await create.click();
  const dialog = page.getByRole("dialog", { name: "Новая задача" });
  await expect(dialog).toBeVisible();
  for (let i = 0; i < 25; i++) {
    await page.keyboard.press("Tab");
    expect(await dialog.evaluate((d) => d.contains(d.ownerDocument.activeElement))).toBe(true);
  }
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(create).toBeFocused();

  // Карточка доски: в порядке Tab — ссылка задачи (без вложенной кнопки-обёртки), Enter открывает.
  const link = page.getByRole("article").filter({ hasText: t.key }).getByRole("link", { name: "Пройти с клавиатуры" });
  await expect(link).toHaveAttribute("aria-roledescription", "перетаскиваемая карточка");
  await expect(page.getByRole("article").filter({ hasText: t.key }).locator('[role="button"]')).toHaveCount(0);
  await link.focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/tasks/${t.key}$`));

  // Вкладки карточки: стрелка вправо выбирает следующую, фокус переходит на неё.
  const tabs = page.getByRole("tablist", { name: "Разделы задачи" });
  const selected = tabs.getByRole("tab", { selected: true });
  await selected.focus();
  const before = await selected.textContent();
  await page.keyboard.press("ArrowRight");
  const now = tabs.getByRole("tab", { selected: true });
  await expect(now).not.toHaveText(before ?? "");
  await expect(now).toBeFocused();
  await expect(page.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", (await now.getAttribute("id")) ?? "");

  // Меню статуса: открыть, стрелкой к пункту, Escape — закрыть и вернуться на статус.
  const status = page.getByLabel(/^Статус: .*Сменить$/);
  await status.focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("ArrowDown");
  await expect(page.locator(".menu-item").first()).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(status).toBeFocused();
  await expect(page.locator(".menu-item").first()).toBeHidden();

  // Фильтр без результатов: сообщение и «Сбросить фильтры».
  await page.goto(`/projects/${key}?q=нет-такой-задачи`);
  await expect(page.getByText("Ничего не найдено по фильтрам.")).toBeVisible();
  await page.getByRole("button", { name: "Сбросить фильтры" }).click();
  await expect(page.getByRole("article").filter({ hasText: t.key })).toBeVisible();
});
