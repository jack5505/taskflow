import { drag, expect, login, test } from "./fixtures.ts";

// ТЗ §7.3: перетаскивание карточки между колонками доски выполняет переход статуса (§4).
test("перетаскивание карточки на доске", async ({ page, api }) => {
  const key = await api.project("Доска");
  const sprint = await api.sprint(key, "Спринт доски");
  const t = await api.task(key, { title: "Сверстать доску", estimate_minutes: 90, sprint_id: sprint.id, assignee_id: api.userId });
  await api.startSprint(sprint.id);

  await login(page);
  await page.goto(`/projects/${key}`);
  const todo = page.getByRole("region", { name: "К выполнению" });
  const inProgress = page.getByRole("region", { name: "В работе" });
  const card = page.getByRole("article").filter({ hasText: t.key });
  await expect(todo.getByRole("article").filter({ hasText: t.key })).toBeVisible();

  await drag(page, card.locator(".task-drag"), inProgress);
  await expect(inProgress.getByRole("article").filter({ hasText: t.key })).toBeVisible();
  await expect(todo.getByRole("article").filter({ hasText: t.key })).toHaveCount(0);

  // Переход сохранён на сервере: после перезагрузки карточка в той же колонке.
  await expect.poll(async () => (await api.getTask(t.key)).status).toBe("in_progress");
  await page.reload();
  await expect(inProgress.getByRole("article").filter({ hasText: t.key })).toBeVisible();
});
