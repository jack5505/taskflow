import { expect, login, test } from "./fixtures.ts";

// ТЗ §6.1–§6.3, §10.4: «45m через веб» к уже списанным 1h 30m (в приёмке — через бот /log,
// здесь через API) — в карточке сумма 2h 15m, запись видна в журнале времени и на доске.
test("списание времени", async ({ page, api }) => {
  const key = await api.project("Учёт времени");
  const sprint = await api.sprint(key, "Спринт учёта");
  const t = await api.taskInProgress(key, { title: "Разобрать логи", estimate_minutes: 240, sprint_id: sprint.id });
  await api.startSprint(sprint.id);
  await api.worklog(t.key, "1h 30m");

  await login(page);
  await page.goto(`/tasks/${t.key}`);
  await expect(page.getByTestId("worklog-spent")).toHaveText("1h 30m");

  await page.getByRole("button", { name: "Списать время" }).click();
  const form = page.getByRole("dialog", { name: "Списать время" });
  // Строка не по формату §6.1 не отправляется.
  await form.getByLabel("Длительность").fill("45 минут");
  await form.getByRole("button", { name: "Списать" }).click();
  await expect(form.getByLabel("Длительность")).toHaveAttribute("aria-invalid", "true");

  await form.getByLabel("Длительность").fill("45m");
  await form.getByLabel("Комментарий").fill("Разбор логов из E2E");
  await form.getByRole("button", { name: "Списать" }).click();
  await expect(form).toBeHidden();

  await expect(page.getByTestId("worklog-spent")).toHaveText("2h 15m");
  await expect(page.getByTestId("worklog-left")).toContainText("1h 45m");
  await page.getByRole("tab", { name: "Журнал времени" }).click();
  await expect(page.getByTestId("worklog-row")).toHaveCount(2);
  await expect(page.getByTestId("worklog-row").filter({ hasText: "Разбор логов из E2E" }).getByTestId("worklog-duration-cell")).toHaveText("45m");
  await expect(page.getByTestId("worklog-total")).toHaveText("2h 15m");

  await page.goto(`/projects/${key}`);
  await expect(page.getByRole("article").filter({ hasText: t.key }).getByTitle("Списано / оценка")).toHaveText("2h 15m / 4h");
});
