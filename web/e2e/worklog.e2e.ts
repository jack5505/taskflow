import { expect, login, test } from "./fixtures.ts";

// ТЗ §6.1–§6.3: списанное время видно в карточке задачи и на доске в формате «2h 15m».
// Формы «Списать время» в интерфейсе пока нет (issue #40) — записи создаются через API,
// проверяется показ. После #40 сценарий переводится на форму.
test("списание времени", async ({ page, api }) => {
  const key = await api.project("Учёт времени");
  const sprint = await api.sprint(key, "Спринт учёта");
  const t = await api.taskInProgress(key, { title: "Разобрать логи", estimate_minutes: 240, sprint_id: sprint.id });
  await api.startSprint(sprint.id);
  await api.worklog(t.key, "1h 30m");
  await api.worklog(t.key, "45m");

  await login(page);
  await page.goto(`/tasks/${t.key}`);
  const field = (name: string) => page.locator("dt", { hasText: new RegExp(`^${name}$`) }).locator("xpath=following-sibling::dd[1]");
  await expect(field("Списано")).toHaveText("2h 15m");
  await expect(field("Остаток")).toHaveText("1h 45m");

  await page.goto(`/projects/${key}`);
  await expect(page.getByRole("article").filter({ hasText: t.key }).getByTitle("Списано / оценка")).toHaveText("2h 15m / 4h");
});
