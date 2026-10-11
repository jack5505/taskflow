import { expect, login, test } from "./fixtures.ts";

// ТЗ §5.4: завершение спринта со сводкой; незакрытые задачи переносятся в выбранный
// запланированный спринт, готовые остаются в закрытом.
test("завершение спринта с переносом задач", async ({ page, api }) => {
  const key = await api.project("Завершение спринта");
  const active = await api.sprint(key, "Спринт 1");
  const done = await api.taskInProgress(key, { title: "Готовая задача", estimate_minutes: 60, sprint_id: active.id });
  const open = [
    await api.task(key, { title: "Незакрытая в очереди", estimate_minutes: 30, sprint_id: active.id }),
    await api.taskInProgress(key, { title: "Незакрытая в работе", estimate_minutes: 45, sprint_id: active.id }),
    await api.taskInProgress(key, { title: "Незакрытая на проверке", estimate_minutes: 15, sprint_id: active.id }),
  ];
  await api.startSprint(active.id);
  await api.transition(open[2].key, "review");
  await api.transition(done.key, "review");
  await api.transition(done.key, "done");
  await api.sprint(key, "Спринт 2");

  await login(page);
  await page.goto(`/projects/${key}/backlog`);
  const first = page.getByRole("region", { name: "Спринт 1" });
  await expect(first.getByText("Активный")).toBeVisible();
  await first.getByRole("button", { name: "Завершить" }).click();

  const dialog = page.getByRole("dialog", { name: "Завершить «Спринт 1»" });
  await expect(dialog.getByText("Готово: 1, незакрыто: 3")).toBeVisible();
  await dialog.getByLabel("Куда перенести незакрытые задачи").selectOption({ label: "В спринт «Спринт 2»" });
  await dialog.getByRole("button", { name: "Завершить" }).click();
  await expect(dialog).toBeHidden();

  // Закрытый спринт уходит из списка, незакрытые — в «Спринт 2», готовая — в архиве.
  await expect(first).toHaveCount(0);
  const second = page.getByRole("region", { name: "Спринт 2" });
  for (const t of open) await expect(second.getByText(t.key)).toBeVisible();
  await expect(page.getByText(done.key)).toHaveCount(0);
  await expect(second.getByText("Задач: 3, оценка 1h 30m")).toBeVisible();

  // Готовая задача осталась в закрытом спринте — видна в его карточке в архиве.
  await page.getByRole("link", { name: "Архив спринтов" }).click();
  await page.getByRole("button", { name: "Спринт 1", exact: true }).click();
  await expect(page.getByRole("region", { name: "Спринт Спринт 1" }).getByText(done.key)).toBeVisible();
});
