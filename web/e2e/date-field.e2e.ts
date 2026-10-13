import type { Page } from "@playwright/test";
import { expect, login, test } from "./fixtures.ts";

// ТЗ §7: даты в интерфейсе — dd.mm.yyyy, в том числе в полях ввода и при английском языке браузера
// (нативное поле даты показало бы mm/dd/yyyy).
test.use({ locale: "en-US" });

/** Был ли PATCH задачи за время действия: недописанная дата не должна ничего отправлять. */
async function patched(page: Page, action: () => Promise<void>) {
  const req = page.waitForRequest((r) => r.method() === "PATCH" && r.url().includes("/api/v1/tasks/"), { timeout: 1500 });
  await action();
  return req.then(
    () => true,
    () => false,
  );
}

test("срок задачи вводится как dd.mm.yyyy", async ({ page, api }) => {
  const key = await api.project("Даты");
  const t = await api.task(key, { title: "Срок в формате ТЗ", due_date: "2026-10-10" });

  await login(page);
  await page.goto(`/tasks/${t.key}`);
  const due = page.getByLabel("Срок");
  await expect(due).toHaveValue("10.10.2026");
  await expect(due).toHaveAttribute("placeholder", "дд.мм.гггг");

  // Стёрли и набрали несуществующую дату: срок не очищается и не меняется, поле красное.
  expect(
    await patched(page, async () => {
      await due.fill("");
      await due.fill("31.02.2026");
      await due.press("Tab");
    }),
  ).toBe(false);
  await expect(due).toHaveAttribute("aria-invalid", "true");
  expect((await api.getTask(t.key)).due_date).toBe("2026-10-10");

  await due.fill("25.10.2026");
  await due.press("Enter");
  await expect.poll(async () => (await api.getTask(t.key)).due_date).toBe("2026-10-25");
  await expect(due).not.toHaveAttribute("aria-invalid", "true");
  await page.reload();
  await expect(page.getByLabel("Срок")).toHaveValue("25.10.2026");
});
