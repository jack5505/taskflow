import { describe, expect, it } from "vitest";
import { describeHistory, formatFieldValue, type ValueContext } from "./history";

const ctx: ValueContext = {
  userName: (id) => ({ u1: "Иван Петров", u2: "Анна" })[id],
  sprintName: (id) => (id === "s1" ? "Спринт 3" : undefined),
  timeZone: "Asia/Tashkent",
};

describe("formatFieldValue", () => {
  it.each([
    ["status", "in_progress", "В работе"],
    ["type", "bug", "Ошибка"],
    ["priority", "critical", "Критический"],
    ["assignee_id", "u1", "Иван Петров"],
    ["assignee_id", "u9", "пользователь не в проекте"],
    ["sprint_id", "s1", "Спринт 3"],
    ["sprint_id", "s9", "другой спринт"],
    ["due_date", "2026-10-31", "31.10.2026"],
    ["estimate_minutes", 150, "2h 30m"],
    ["labels", ["api", "ui"], "api, ui"],
    ["title", "Вход", "Вход"],
    ["deleted_at", "2026-10-09T21:30:00Z", "10.10.2026 02:30"],
  ])("%s = %j → %s", (field, v, want) => {
    expect(formatFieldValue(field, v, ctx)).toBe(want);
  });

  it("пустое — прочерк", () => {
    for (const v of [null, undefined, "", []]) expect(formatFieldValue("due_date", v, ctx)).toBe("—");
  });

  it("длинное описание сокращается", () => {
    expect(formatFieldValue("description", "а".repeat(300), ctx)).toBe(`${"а".repeat(200)}…`);
  });
});

describe("describeHistory", () => {
  it("изменение поля", () => {
    expect(
      describeHistory(
        {
          id: "h1",
          field: "status",
          old_value: "todo",
          new_value: "in_progress",
          user_id: "u1",
          source: "telegram",
          created_at: "2026-10-09T14:30:00Z",
        },
        ctx,
      ),
    ).toEqual({
      id: "h1",
      who: "Иван Петров",
      when: "09.10.2026 19:30",
      text: "Статус: К выполнению → В работе",
      note: undefined,
      source: "из Telegram",
    });
  });

  it("снятие исполнителя с пометкой", () => {
    const line = describeHistory(
      { id: "h2", field: "assignee_id", old_value: "u2", new_value: null, note: "снят при деактивации", source: "api" },
      ctx,
    );
    expect(line.text).toBe("Исполнитель: Анна → —");
    expect(line.note).toBe("снят при деактивации");
    expect(line.who).toBe("Система");
    expect(line.source).toBe("через API");
  });

  it("создание, удаление и восстановление", () => {
    expect(describeHistory({ field: "status", old_value: null, new_value: "todo", source: "web" }, ctx).text).toBe(
      "Задача создана: К выполнению",
    );
    expect(describeHistory({ field: "deleted_at", new_value: "2026-10-09T14:30:00Z" }, ctx).text).toBe("Задача удалена");
    expect(describeHistory({ field: "deleted_at", old_value: "2026-10-09T14:30:00Z" }, ctx).text).toBe(
      "Задача восстановлена",
    );
  });

  it("оценка, срок и неизвестное поле", () => {
    expect(describeHistory({ field: "estimate_minutes", old_value: null, new_value: 90 }, ctx).text).toBe(
      "Оценка: — → 1h 30m",
    );
    expect(describeHistory({ field: "due_date", old_value: "2026-10-01", new_value: "2026-10-05" }, ctx).text).toBe(
      "Срок: 01.10.2026 → 05.10.2026",
    );
    expect(describeHistory({ field: "weird", old_value: 1, new_value: { a: 1 } }, ctx).text).toBe('weird: 1 → {"a":1}');
  });
});
