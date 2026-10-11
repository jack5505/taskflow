import { expect, login, test } from "./fixtures.ts";

// ТЗ §6.4: таймер запускается в карточке задачи, виден в шапке на всех страницах
// (ключ задачи, прошедшее время, «Стоп») и останавливается из шапки.
test("таймер: старт, шапка, стоп", async ({ page, api }) => {
  const key = await api.project("Таймер");
  const sprint = await api.sprint(key, "Спринт таймера");
  const t = await api.taskInProgress(key, { title: "Починить импорт", estimate_minutes: 60, sprint_id: sprint.id });
  await api.startSprint(sprint.id);

  await login(page);
  await page.goto(`/tasks/${t.key}`);
  await page.getByRole("button", { name: `Запустить таймер ${t.key}` }).click();
  await expect(page.getByRole("button", { name: `Остановить таймер ${t.key}` })).toBeVisible();

  const header = page.getByRole("banner");
  await expect(header.getByRole("link", { name: t.key })).toBeVisible();
  await expect(header.getByLabel("Прошло времени")).toHaveText(/^\d+:\d{2}(:\d{2})?$/);

  // Шапка общая для всех страниц: на отчётах таймер тоже виден, на доске — индикатор у карточки.
  await page.goto("/reports");
  await expect(header.getByRole("link", { name: t.key })).toBeVisible();
  await page.goto(`/projects/${key}`);
  await expect(page.getByRole("article").filter({ hasText: t.key }).getByLabel("Идёт таймер")).toBeVisible();

  await header.getByRole("button", { name: "Стоп" }).click();
  // Меньше минуты — запись не создаётся (§6.4), об этом сообщение в шапке.
  await expect(header.getByRole("status")).toHaveText(`${t.key}: меньше минуты, время не списано`);
  await expect(header.getByRole("link", { name: t.key })).toHaveCount(0);
  await expect(page.getByRole("article").filter({ hasText: t.key }).getByRole("button", { name: `Запустить таймер ${t.key}` })).toBeVisible();
});
