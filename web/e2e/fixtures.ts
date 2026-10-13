import { expect, test as base, type APIRequestContext, type Locator, type Page } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD, BASE_URL } from "./env.ts";

// Подготовка данных через API: каждый сценарий заводит свой проект, поэтому сценарии не
// зависят друг от друга и от порядка запуска. Проверяется только интерфейс.

export interface Task {
  id: string;
  key: string;
  status: string;
}

export interface Sprint {
  id: string;
  name: string;
  state: string;
}

export class Api {
  private readonly ctx: APIRequestContext;
  readonly userId: string;

  constructor(ctx: APIRequestContext, userId: string) {
    this.ctx = ctx;
    this.userId = userId;
  }

  private async call<T>(method: "GET" | "POST" | "PUT" | "PATCH", url: string, data?: unknown): Promise<T> {
    const r = await this.ctx.fetch(url, { method, data });
    if (!r.ok()) throw new Error(`${method} ${url}: ${r.status()} ${await r.text()}`);
    const text = await r.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  /** Ответ без проверки статуса — для сценариев, где сервер должен отказать. */
  raw(method: "POST", url: string, data?: unknown) {
    return this.ctx.fetch(url, { method, data });
  }

  /** Проект со случайным ключом; администратор E2E в нём менеджер (может быть исполнителем). */
  async project(name: string): Promise<string> {
    const key = `E${Array.from({ length: 6 }, () => String.fromCharCode(65 + Math.floor(Math.random() * 26))).join("")}`;
    await this.call("POST", "/api/v1/projects", { key, name, timezone: "UTC" });
    await this.call("PUT", `/api/v1/projects/${key}/members/${this.userId}`, { role: "manager" });
    return key;
  }

  task(project: string, fields: { title: string; estimate_minutes?: number; sprint_id?: string; assignee_id?: string; due_date?: string }) {
    return this.call<Task>("POST", `/api/v1/projects/${project}/tasks`, fields);
  }

  /** Задача на себе в статусе «В работе». */
  async taskInProgress(project: string, fields: { title: string; estimate_minutes?: number; sprint_id?: string }) {
    const t = await this.task(project, { ...fields, assignee_id: this.userId });
    return this.transition(t.key, "in_progress");
  }

  transition(key: string, to: string) {
    return this.call<Task>("POST", `/api/v1/tasks/${key}/transition`, { to });
  }

  /** Спринт на две недели с сегодняшнего дня (по UTC — пояс проекта). */
  sprint(project: string, name: string) {
    const start = new Date();
    const end = new Date(start.getTime() + 14 * 86_400_000);
    const day = (d: Date) => d.toISOString().slice(0, 10);
    return this.call<Sprint>("POST", `/api/v1/projects/${project}/sprints`, { name, start_date: day(start), end_date: day(end) });
  }

  startSprint(id: string) {
    return this.call("POST", `/api/v1/sprints/${id}/start`);
  }

  getTask(key: string) {
    return this.call<Task & { sprint_id?: string; spent_minutes?: number; due_date?: string }>("GET", `/api/v1/tasks/${key}`);
  }

  worklog(key: string, duration: string) {
    return this.call("POST", `/api/v1/tasks/${key}/worklogs`, { duration });
  }
}

export const test = base.extend<{ api: Api }>({
  api: async ({ playwright }, provide) => {
    const anon = await playwright.request.newContext({ baseURL: BASE_URL });
    const r = await anon.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    expect(r.ok(), await r.text()).toBeTruthy();
    const s = (await r.json()) as { access_token: string; user: { id: string } };
    await anon.dispose();
    const ctx = await playwright.request.newContext({
      baseURL: BASE_URL,
      extraHTTPHeaders: { Authorization: `Bearer ${s.access_token}` },
    });
    await provide(new Api(ctx, s.user.id));
    await ctx.dispose();
  },
});

export { expect };

/** Вход через форму (ТЗ §7.1). Токен доступа живёт в памяти вкладки, поэтому — в каждом тесте. */
export async function login(page: Page, email = ADMIN_EMAIL, password = ADMIN_PASSWORD) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByRole("button", { name: "Выйти" })).toBeVisible();
}

/**
 * Перетаскивание мышью для dnd-kit: PointerSensor срабатывает после сдвига на 5 px, а цель
 * определяется по промежуточным движениям — поэтому шагами, а не одним прыжком.
 */
export async function drag(page: Page, source: Locator, target: Locator) {
  const from = await source.boundingBox();
  const to = await target.boundingBox();
  if (!from || !to) throw new Error("drag: элемент не на экране");
  await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
  await page.mouse.down();
  await page.mouse.move(from.x + from.width / 2 + 10, from.y + from.height / 2 + 10, { steps: 5 });
  await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 20 });
  await page.mouse.up();
}
