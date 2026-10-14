import type { Locator, Page } from "@playwright/test";
import { ADMIN_EMAIL } from "./env.ts";
import { expect, login, test } from "./fixtures.ts";

// ТЗ §7, §11 п. 14: интерфейс работает с телефона (от 360 px). На каждом экране страница не
// прокручивается по горизонтали — широкое (таблицы, колонки доски) прокручивается внутри своего блока.
test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true });

const PHONE = 375;

// Документ шире телефона — страница уезжает вбок. Окно эмуляции растягивается под содержимое,
// поэтому сравнение с шириной телефона, а не с innerWidth. Строкой: в tsconfig e2e нет DOM.
const overflow = (page: Page) => page.evaluate<number>(`document.documentElement.scrollWidth - ${PHONE}`);

// Элементы, вылезающие за ширину телефона не внутри своего прокручиваемого блока, — чтобы
// сообщение об ошибке сразу называло виновника.
const offenders = (page: Page) =>
  page.evaluate<string>(`[...document.querySelectorAll("body *")]
    .filter((e) => {
      if (e.getBoundingClientRect().right <= ${PHONE} + 1) return false;
      for (let a = e.parentElement; a && a !== document.body; a = a.parentElement)
        if (getComputedStyle(a).overflowX !== "visible") return false;
      return true;
    })
    .slice(0, 5).map((e) => e.tagName.toLowerCase() + [...e.classList].map((c) => "." + c).join("")).join(", ")`);

/** Экран загружен (ready виден, индикаторов загрузки нет) — снимок и проверка ширины. */
async function screen(page: Page, name: string, ready: Locator) {
  await expect(ready).toBeVisible();
  await expect(page.locator(".loading")).toHaveCount(0);
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
  const invite = await api.raw("POST", `/api/v1/projects/${key}/invites`, { role: "member" });
  const joinPath = new URL(((await invite.json()) as { url: string }).url).pathname;

  await page.goto("/login");
  await screen(page, "01-login", page.getByRole("button", { name: "Войти" }));
  await page.goto(joinPath);
  await screen(page, "01-join", page.getByRole("heading", { name: /Приглашение в проект/ }));

  await login(page);
  // Таймер в шапке — самая широкая её часть: экраны дальше проверяются с ним.
  await api.raw("POST", `/api/v1/tasks/${t.key}/timer/start`);
  try {
    await page.goto("/");
    await screen(page, "02-projects", page.getByRole("heading", { name: "Мои задачи" }));
    await expect(page.getByRole("banner").getByRole("button", { name: "Стоп" })).toBeVisible();

    await page.goto(`/projects/${key}`);
    await screen(page, "03-board", page.getByRole("article").filter({ hasText: t.key }));
    await page.locator(".board-filters summary").first().tap();
    await screen(page, "03-board-filter", page.locator(".board-filters .dropdown-menu").first());

    await page.goto(`/projects/${key}/backlog`);
    await screen(page, "04-backlog", page.getByRole("region", { name: "Бэклог" }));
    await page.goto(`/projects/${key}/backlog/archive`);
    await screen(page, "04-archive", page.getByText("Архив спринтов"));

    await page.goto(`/tasks/${t.key}`);
    await screen(page, "05-task", page.getByText("Проверить вёрстку на телефоне"));
    await page.getByRole("tab", { name: "Журнал времени" }).tap();
    await screen(page, "05-task-worklogs", page.getByTestId("worklog-list"));

    await page.goto("/reports");
    await screen(page, "06-reports", page.locator(".report-table"));

    await page.goto("/profile");
    await screen(page, "07-profile", page.getByRole("heading", { name: "Профиль" }));

    const tabs: [string, Locator][] = [
      ["general", page.getByLabel("Название")],
      ["members", page.getByRole("cell", { name: ADMIN_EMAIL })],
      ["telegram", page.getByRole("heading", { name: "Telegram-группа" })],
      ["notifications", page.getByRole("heading", { name: "Сообщения бота в группу" })],
      ["audit", page.getByRole("table")],
      ["trash", page.getByText("Корзина пуста.")],
    ];
    for (const [tab, ready] of tabs) {
      await page.goto(`/projects/${key}/settings?tab=${tab}`);
      await screen(page, `08-settings-${tab}`, ready);
    }

    await page.goto(`/projects/${key}`);
    await page.getByRole("button", { name: "Уведомления" }).tap();
    await screen(page, "09-bell", page.locator(".bell-panel"));
    await page.getByRole("button", { name: "Уведомления" }).tap();
    await page.getByRole("button", { name: "Создать задачу" }).tap();
    await screen(page, "10-create-task", page.getByRole("dialog"));
  } finally {
    await api.raw("POST", "/api/v1/me/timer/stop");
  }
});

// Основные действия пальцем: смена статуса через меню, списание времени, перетаскивание карточки
// долгим нажатием (ТЗ §7: касание прокручивает, долгое нажатие перетаскивает).
test("работа с задачей с телефона", async ({ page, api }) => {
  const key = await api.project("Телефон");
  const sprint = await api.sprint(key, "Спринт");
  const t = await api.taskInProgress(key, { title: "Сделать с телефона", estimate_minutes: 60, sprint_id: sprint.id });
  const todo = await api.task(key, { title: "Перетащить пальцем", sprint_id: sprint.id, assignee_id: api.userId });
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

  // Долгое нажатие на карточке «К выполнению» и перенос во вторую колонку (видна краем справа).
  // У Playwright нет жеста «нажать и вести» — касания через CDP.
  await page.goto(`/projects/${key}`);
  const card = page.getByRole("article").filter({ hasText: todo.key });
  await expect(card).toBeVisible();
  const box = (await card.boundingBox())!;
  const column = (await page.getByRole("region", { name: "В работе" }).boundingBox())!;
  const from = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  const to = { x: Math.min(column.x + 40, PHONE - 10), y: column.y + 80 };
  const cdp = await page.context().newCDPSession(page);
  const touch = (type: "touchStart" | "touchMove" | "touchEnd", p?: { x: number; y: number }) =>
    cdp.send("Input.dispatchTouchEvent", { type, touchPoints: p ? [p] : [] });
  await touch("touchStart", from);
  // Именно пауза, а не ожидание условия: проверяется, что жест «долгое нажатие» (250 мс) начинает перенос.
  await page.waitForTimeout(400);
  for (let i = 1; i <= 10; i++) await touch("touchMove", { x: from.x + ((to.x - from.x) * i) / 10, y: from.y + ((to.y - from.y) * i) / 10 });
  await touch("touchEnd");
  await expect.poll(async () => (await api.getTask(todo.key)).status).toBe("in_progress");
});
