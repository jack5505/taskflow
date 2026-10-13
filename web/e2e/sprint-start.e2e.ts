import { drag, expect, login, test } from "./fixtures.ts";

// ТЗ §5.1, §5.2, §7.4: менеджер создаёт спринт, перетаскивает в него задачи из бэклога и
// запускает его; второй спринт при активном запустить нельзя.
test("создание и запуск спринта", async ({ page, api }) => {
  const key = await api.project("Запуск спринта");
  const first = await api.task(key, { title: "Настроить сборку", estimate_minutes: 120 });
  const second = await api.task(key, { title: "Описать API", estimate_minutes: 60 });

  await login(page);
  await page.goto(`/projects/${key}/backlog`);
  const backlog = page.getByRole("region", { name: "Бэклог" });
  await expect(backlog.getByText(first.key)).toBeVisible();
  // Стили бэклога применились (#174: потерянная скобка в index.css отключала их целиком).
  await expect(backlog.locator("li", { hasText: first.key })).toHaveCSS("display", "grid");
  await expect(page.locator(".topbar-link")).toHaveCSS("margin-left", "24px");

  await page.getByRole("button", { name: "Создать спринт" }).click();
  const create = page.getByRole("dialog", { name: "Новый спринт" });
  await create.getByLabel("Название").fill("Спринт E2E");
  await create.getByLabel("Цель").fill("Первый релиз");
  await create.getByRole("button", { name: "Создать" }).click();
  await expect(create).toBeHidden();

  const sprint = page.getByRole("region", { name: "Спринт E2E" });
  await expect(sprint.getByText("Перетащите сюда задачи из бэклога")).toBeVisible();
  for (const t of [first, second]) {
    await drag(page, backlog.locator("li", { hasText: t.key }), sprint);
    await expect(sprint.getByText(t.key)).toBeVisible();
  }
  await expect(sprint.getByText("Задач: 2, оценка 3h")).toBeVisible();
  await expect(backlog.getByText("Задач нет")).toBeVisible();

  await sprint.getByRole("button", { name: "Запустить" }).click();
  const start = page.getByRole("dialog", { name: "Запустить «Спринт E2E»" });
  await expect(start.getByText("Задач: 2, оценка 3h")).toBeVisible();
  // Даты по умолчанию — сегодня и через 14 дней.
  await start.getByRole("button", { name: "Запустить" }).click();
  await expect(start).toBeHidden();
  await expect(sprint.getByText("Активный")).toBeVisible();

  // Перетаскивание сохранилось на сервере.
  for (const t of [first, second]) await expect.poll(async () => (await api.getTask(t.key)).sprint_id).toBeTruthy();

  // Второй спринт: кнопки «Запустить» нет, а сервер отказывает с 409.
  const next = await api.sprint(key, "Спринт 2");
  await api.task(key, { title: "Задача второго спринта", sprint_id: next.id });
  await page.reload();
  const nextSection = page.getByRole("region", { name: "Спринт 2" });
  await expect(nextSection.getByRole("button", { name: "Удалить" })).toBeVisible();
  await expect(nextSection.getByRole("button", { name: "Запустить" })).toHaveCount(0);
  const r = await api.raw("POST", `/api/v1/sprints/${next.id}/start`);
  expect(r.status()).toBe(409);
});
