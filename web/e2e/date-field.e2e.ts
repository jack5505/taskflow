import { expect, login, test } from "./fixtures.ts";

// ТЗ §7: даты в интерфейсе — dd.mm.yyyy, в том числе в полях ввода и при английском языке браузера
// (нативное поле даты показало бы mm/dd/yyyy).
test.use({ locale: "en-US" });

test("срок задачи вводится как dd.mm.yyyy", async ({ page, api }) => {
  const key = await api.project("Даты");
  const t = await api.task(key, { title: "Срок в формате ТЗ" });

  await login(page);
  await page.goto(`/tasks/${t.key}`);
  const due = page.getByLabel("Срок");
  await expect(due).toHaveAttribute("placeholder", "дд.мм.гггг");

  // Несуществующая дата не сохраняется и подсвечивается.
  await due.fill("31.02.2026");
  await due.press("Enter");
  await expect(due).toHaveAttribute("aria-invalid", "true");
  expect((await api.getTask(t.key)).due_date ?? "").toBe("");

  await due.fill("25.10.2026");
  await due.press("Enter");
  await expect.poll(async () => (await api.getTask(t.key)).due_date).toBe("2026-10-25");
  await page.reload();
  await expect(page.getByLabel("Срок")).toHaveValue("25.10.2026");
});
