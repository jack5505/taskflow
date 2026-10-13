import { expect, login, test } from "./fixtures.ts";

// ТЗ §7: подтверждение своим окном (не window.confirm), после действия — сообщение; удаление задачи
// администратор может отменить из сообщения (восстановление — его право, §4).
test("удаление задачи: подтверждение, сообщение и «Отменить»", async ({ page, api }) => {
  const key = await api.project("Подтверждения");
  const t = await api.task(key, { title: "Удалить и вернуть" });
  page.on("dialog", (d) => {
    throw new Error(`браузерное окно вместо своего: ${d.message()}`);
  });

  await login(page);
  await page.goto(`/tasks/${t.key}`);
  await page.getByRole("button", { name: "Удалить", exact: true }).click();
  const confirm = page.getByRole("dialog", { name: `Удалить задачу ${t.key}?` });
  await expect(confirm).toBeVisible();
  await expect(confirm.getByRole("button", { name: "Удалить" })).toBeFocused();

  // Escape — отмена: окно закрыто, задача на месте, фокус вернулся на кнопку.
  await page.keyboard.press("Escape");
  await expect(confirm).toBeHidden();
  await expect(page.getByRole("button", { name: "Удалить", exact: true })).toBeFocused();

  await page.getByRole("button", { name: "Удалить", exact: true }).click();
  await confirm.getByRole("button", { name: "Удалить" }).click();
  await expect(page).toHaveURL(new RegExp(`/projects/${key}$`));
  const status = page.getByRole("status").filter({ hasText: `Задача ${t.key} удалена` });
  await expect(status).toBeVisible();

  await status.getByRole("button", { name: "Отменить" }).click();
  await expect(page.getByRole("status").filter({ hasText: `Задача ${t.key} восстановлена` })).toBeVisible();
  await expect.poll(async () => (await api.getTask(t.key)).key).toBe(t.key);
});
