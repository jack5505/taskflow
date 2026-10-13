import { expect, login, test } from "./fixtures.ts";

// ТЗ §10.3: Content-Security-Policy без inline-скриптов и стилей. Сообщения браузера о нарушениях
// политики и ошибки страниц на основных экранах и в Swagger UI — провал: значит, что-то не загрузилось.
test("нарушений Content-Security-Policy нет", async ({ page, api }) => {
  const key = await api.project("Политика");
  const sprint = await api.sprint(key, "Спринт политики");
  const t = await api.taskInProgress(key, { title: "Проверить заголовки", estimate_minutes: 60, sprint_id: sprint.id });
  await api.startSprint(sprint.id);

  const problems: string[] = [];
  page.on("console", (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) problems.push(`${page.url()}: ${m.text()}`);
  });
  page.on("pageerror", (e) => problems.push(`${page.url()}: ${e.message}`));

  await login(page);
  const response = await page.goto(`/projects/${key}`);
  expect(response?.headers()["content-security-policy"]).toContain("script-src 'self';");
  await expect(page.getByText(t.key)).toBeVisible();

  await page.goto(`/tasks/${t.key}`);
  await expect(page.locator(".task-heading")).toContainText("Проверить заголовки");
  await page.goto(`/projects/${key}/backlog`);
  await expect(page.getByText(t.key)).toBeVisible();
  await page.goto("/reports");
  await expect(page.getByRole("button", { name: "Скачать CSV" })).toBeVisible();
  await page.goto("/profile");
  await expect(page.getByRole("button", { name: "Сохранить" }).first()).toBeVisible();
  await page.goto("/api/docs/index.html");
  await expect(page.locator(".swagger-ui .info")).toBeVisible();

  expect(problems).toEqual([]);
});
