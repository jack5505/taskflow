import { expect, login, test } from "./fixtures.ts";

// #174: потерянная закрывающая скобка в index.css превращала все следующие правила во вложенные,
// и браузер молча их не применял. Сборка и stylelint ловят это заранее, а здесь — что разделы,
// которые тогда отвалились, действительно оформлены: бэклог, шапка, отчёты, профиль.
test("стили бэклога, шапки, отчётов и профиля применяются", async ({ page, api }) => {
  const key = await api.project("Стили");
  const sprint = await api.sprint(key, "Спринт стилей");
  const t = await api.taskInProgress(key, { title: "Проверить стили", estimate_minutes: 60, sprint_id: sprint.id });
  await api.startSprint(sprint.id);
  await api.worklog(t.key, "30m");

  await login(page);
  await page.goto(`/projects/${key}/backlog`);
  await expect(page.locator(".backlog-row", { hasText: t.key })).toHaveCSS("display", "grid");
  // Текущий раздел подчёркнут цветом акцента (--accent).
  await expect(page.locator(".topbar-nav a.active")).toHaveCSS("border-bottom-color", "rgb(9, 105, 218)");

  await page.goto("/reports");
  await expect(page.locator(".report-hours").first()).toHaveCSS("display", "block");

  await page.goto("/profile");
  await expect(page.locator(".profile-page .panel").first()).not.toHaveCSS("max-width", "none");
});
