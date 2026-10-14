import { expect, login, test } from "./fixtures.ts";

// ТЗ §7.1, §7.9: менеджер создаёт ссылку-приглашение, новый человек по ней сам регистрируется
// и сразу попадает в проект; уже зарегистрированный присоединяется кнопкой; отозванная ссылка
// не действует.
test("регистрация по ссылке-приглашению", async ({ page, browser, api }) => {
  const key = await api.project("Приглашения");
  const other = await api.project("Второй проект");

  await login(page);
  await page.goto(`/projects/${key}/settings?tab=members`);
  const panel = page.getByRole("region", { name: "Ссылки-приглашения" });
  await panel.getByRole("button", { name: "Создать ссылку" }).click();
  const link = panel.getByRole("textbox", { name: /Новая ссылка/ });
  await expect(link).toHaveValue(/\/join\//);
  const path = new URL(await link.inputValue()).pathname;
  await expect(panel.getByRole("table", { name: "Действующие ссылки" }).getByRole("row")).toHaveCount(2);

  // Новый человек — в отдельном браузере без сессии.
  const guest = await browser.newContext();
  const g = await guest.newPage();
  await g.goto(path);
  await expect(g.getByRole("heading", { name: "Приглашение в проект «Приглашения»" })).toBeVisible();
  await expect(g.getByText("исполнитель")).toBeVisible();
  const email = `invited-${Date.now()}@example.com`;
  await g.getByLabel("Имя").fill("Приглашённый Участник");
  await g.getByLabel("Email").fill(email);
  await g.getByLabel(/^Пароль/).fill("invited-password");
  await g.getByLabel("Повторите пароль").fill("invited-password");
  await g.getByRole("button", { name: "Зарегистрироваться и присоединиться" }).click();
  await expect(g).toHaveURL(new RegExp(`/projects/${key}$`));
  await expect(g.getByRole("heading", { name: /Приглашения/ })).toBeVisible();

  // Он же по ссылке второго проекта — уже с сессией: одна кнопка.
  const r = await api.raw("POST", `/api/v1/projects/${other}/invites`, { role: "member" });
  const otherPath = new URL(((await r.json()) as { url: string }).url).pathname;
  await g.goto(otherPath);
  await expect(g.getByText(`Вы вошли как Приглашённый Участник (${email})`)).toBeVisible();
  await g.getByRole("button", { name: "Присоединиться к проекту" }).click();
  await expect(g).toHaveURL(new RegExp(`/projects/${other}$`));

  // Менеджер видит нового участника и счётчик; отзывает ссылку — по ней больше не войти.
  await page.reload();
  await expect(page.getByRole("cell", { name: email })).toBeVisible();
  await expect(panel.getByRole("cell", { name: "1", exact: true })).toBeVisible();
  await panel.getByRole("button", { name: "Отозвать" }).click();
  await page.getByRole("dialog", { name: "Отозвать ссылку?" }).getByRole("button", { name: "Отозвать" }).click();
  await expect(panel.getByRole("table", { name: "Действующие ссылки" })).toHaveCount(0);

  const late = await browser.newContext();
  const l = await late.newPage();
  await l.goto(path);
  await expect(l.getByRole("alert")).toHaveText(/Ссылка-приглашение недействительна/);
  await guest.close();
  await late.close();
});
