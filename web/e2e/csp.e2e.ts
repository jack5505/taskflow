import { drag, expect, login, test } from "./fixtures.ts";

// События securitypolicyviolation каждой страницы складываются в window, тест их читает. Строкой:
// tsconfig e2e без DOM-типов.
const LISTEN = `window.__csp = [];
document.addEventListener("securitypolicyviolation", (e) =>
  window.__csp.push(location.pathname + ": " + e.violatedDirective + " " + e.blockedURI));`;

// ТЗ §10.3: Content-Security-Policy без inline-скриптов и стилей. Нарушение политики (событие
// securitypolicyviolation) или ошибка страницы на основных экранах, при перетаскивании, в редакторе
// Markdown и в Swagger UI — провал: значит, что-то не загрузилось или не сработало.
test("нарушений Content-Security-Policy нет", async ({ page, api }) => {
  const key = await api.project("Политика");
  const sprint = await api.sprint(key, "Спринт политики");
  const t = await api.task(key, { title: "Проверить заголовки", estimate_minutes: 60, sprint_id: sprint.id, assignee_id: api.userId });
  await api.startSprint(sprint.id);

  await page.addInitScript({ content: LISTEN });
  const problems: string[] = [];
  page.on("pageerror", (e) => problems.push(`${page.url()}: ${e.message}`));
  const collect = async () => problems.push(...((await page.evaluate("window.__csp ?? []")) as string[]));

  await login(page);
  const response = await page.goto(`/projects/${key}`);
  expect(response?.headers()["content-security-policy"]).toContain("script-src 'self';");
  const card = page.getByRole("article").filter({ hasText: t.key });
  await drag(page, card.locator(".task-drag"), page.getByRole("region", { name: "В работе" }));
  await expect(page.getByRole("region", { name: "В работе" }).getByRole("article").filter({ hasText: t.key })).toBeVisible();
  await collect();

  await page.goto(`/tasks/${t.key}`);
  await expect(page.locator(".task-heading")).toContainText("Проверить заголовки");
  await page.getByRole("button", { name: "Изменить описание" }).click();
  await page.locator(".md-editor textarea").first().fill("**жирный** и `код`");
  await page.locator(".md-editor").first().getByRole("tab", { name: "Предпросмотр" }).click();
  await expect(page.locator(".md-preview strong")).toHaveText("жирный");
  await collect();

  for (const path of [`/projects/${key}/backlog`, "/reports", "/profile"]) {
    await page.goto(path);
    await expect(page.getByRole("button", { name: "Выйти", exact: true })).toBeVisible();
    await collect();
  }
  await page.goto("/api/docs/index.html");
  await expect(page.locator(".swagger-ui .info")).toBeVisible();
  await page.locator(".opblock-summary").first().click();
  await expect(page.locator(".opblock-body").first()).toBeVisible();
  await collect();

  expect(problems).toEqual([]);
});
