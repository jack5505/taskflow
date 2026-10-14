import type { Page } from "@playwright/test";
import { expect, login, test } from "./fixtures.ts";

// ТЗ §7, §11 п. 14: интерфейс работает с телефона (от 360 px). На каждом экране страница не
// прокручивается по горизонтали — широкое (таблицы, колонки доски) прокручивается внутри своего блока.
test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true });

// Ширина документа больше окна — значит, страница уезжает вбок. Строкой: в tsconfig e2e нет DOM.
const overflow = (page: Page) =>
  page.evaluate<number>("document.documentElement.scrollWidth - document.documentElement.clientWidth");

// Элементы, вылезающие за ширину телефона (375 px; окно эмуляции растягивается под содержимое,
// поэтому не innerWidth) не внутри своего прокручиваемого блока, — чтобы сообщение
// об ошибке сразу называло виновника.
const offenders = (page: Page) =>
  page.evaluate<string>(`[...document.querySelectorAll("body *")]
    .filter((e) => {
      if (e.getBoundingClientRect().right <= 375 + 1) return false;
      for (let a = e.parentElement; a && a !== document.body; a = a.parentElement)
        if (getComputedStyle(a).overflowX !== "visible") return false;
      return true;
    })
    .slice(0, 5).map((e) => e.tagName.toLowerCase() + [...e.classList].map((c) => "." + c).join("")).join(", ")`);

async function screen(page: Page, name: string) {
  await page.screenshot({ path: `test-results/mobile/${name}.png`, fullPage: true });
  const over = await overflow(page);
  expect.soft(over, `${name}: горизонтальная прокрутка страницы (${over > 0 ? await offenders(page) : ""})`).toBeLessThanOrEqual(0);
}

test("экраны на телефоне без горизонтальной прокрутки", async ({ page, api }) => {
  const key = await api.project("Мобильный проект с длинным названием");
  const sprint = await api.sprint(key, "Спринт с телефона");
  const t = await api.taskInProgress(key, { title: "Проверить вёрстку на телефоне", estimate_minutes: 120, sprint_id: sprint.id });
  await api.task(key, { title: "Вторая задача в бэклоге с довольно длинным названием", estimate_minutes: 30 });
  await api.startSprint(sprint.id);
  await api.worklog(t.key, "45m");

  await page.goto("/login");
  await screen(page, "01-login");
  await login(page);
  await expect(page.getByRole("heading", { name: "Мои задачи" })).toBeVisible();
  await screen(page, "02-projects");

  await page.goto(`/projects/${key}`);
  await expect(page.getByText(t.key).first()).toBeVisible();
  await screen(page, "03-board");

  await page.goto(`/projects/${key}/backlog`);
  await expect(page.getByRole("region", { name: "Бэклог" })).toBeVisible();
  await screen(page, "04-backlog");

  await page.goto(`/tasks/${t.key}`);
  await expect(page.getByText("Проверить вёрстку на телефоне")).toBeVisible();
  await screen(page, "05-task");

  await page.goto("/reports");
  await screen(page, "06-reports");

  await page.goto("/profile");
  await screen(page, "07-profile");

  for (const tab of ["general", "members", "notifications", "audit", "trash"]) {
    await page.goto(`/projects/${key}/settings?tab=${tab}`);
    await expect(page.getByRole("tab", { selected: true })).toBeVisible();
    await screen(page, `08-settings-${tab}`);
  }

  await page.goto(`/projects/${key}`);
  await page.keyboard.press("c");
  await expect(page.getByRole("dialog")).toBeVisible();
  await screen(page, "09-create-task");
});

// Основной сценарий пальцем: карточка задачи — смена статуса через меню и списание времени.
test("работа с задачей с телефона", async ({ page, api }) => {
  const key = await api.project("Телефон");
  const sprint = await api.sprint(key, "Спринт");
  const t = await api.taskInProgress(key, { title: "Сделать с телефона", estimate_minutes: 60, sprint_id: sprint.id });
  await api.startSprint(sprint.id);

  await login(page);
  await page.goto(`/tasks/${t.key}`);
  await page.getByLabel(/^Статус: .*Сменить$/).tap();
  await page.getByRole("button", { name: "На проверке" }).tap();
  await expect(page.getByLabel(/^Статус: На проверке/)).toBeVisible();

  await page.getByRole("button", { name: "Списать время" }).tap();
  const form = page.getByRole("dialog", { name: "Списать время" });
  await form.getByLabel("Длительность").fill("30m");
  await form.getByRole("button", { name: "Списать" }).tap();
  await expect(form).toBeHidden();
  await expect(page.getByTestId("worklog-spent")).toHaveText("30m");
  expect(await overflow(page)).toBeLessThanOrEqual(0);
});
