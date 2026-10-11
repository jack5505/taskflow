import { describe, expect, it } from "vitest";
import {
  AUDIT_ACTIONS,
  auditActionLabel,
  auditChanges,
  auditEntity,
  auditQuery,
  changedEvents,
  effectiveEvents,
  isValidTime,
  memberErrorText,
  normalizeTime,
  tabFromParam,
  trashDeleter,
  visibleTabs,
} from "./logic";

/** Ответ API с ошибкой, как его бросает сгенерированный клиент. */
function apiError(status: number, code: string, message = "", details: Record<string, unknown> = {}) {
  const r = new Response(null, { status }) as Response & { error: unknown };
  r.error = { error: { code, message, details } };
  return r;
}

describe("вкладки настроек (§7.9, §2)", () => {
  it("администратор видит все шесть", () => {
    expect(visibleTabs({ manager: true, admin: true })).toEqual([
      "general",
      "members",
      "telegram",
      "notifications",
      "audit",
      "trash",
    ]);
    expect(visibleTabs({ manager: false, admin: true })).toHaveLength(6);
  });

  it("менеджер не видит Журнал и Корзину", () => {
    const tabs = visibleTabs({ manager: true, admin: false });
    expect(tabs).toEqual(["general", "members", "telegram", "notifications"]);
    expect(tabs).not.toContain("audit");
    expect(tabs).not.toContain("trash");
  });

  it("исполнителю настройки недоступны", () => {
    expect(visibleTabs({ manager: false, admin: false })).toEqual([]);
  });

  it("вкладка из адреса: недоступная заменяется первой", () => {
    const manager = visibleTabs({ manager: true, admin: false });
    expect(tabFromParam("members", manager)).toBe("members");
    expect(tabFromParam("trash", manager)).toBe("general");
    expect(tabFromParam(null, manager)).toBe("general");
    expect(tabFromParam("audit", [])).toBeNull();
  });
});

describe("ошибка LAST_MANAGER (§9.3)", () => {
  const last = apiError(409, "LAST_MANAGER", "Это единственный активный менеджер проекта");

  it("понижение и исключение — понятный текст с подсказкой", () => {
    expect(memberErrorText(last, "role")).toBe(
      "Нельзя понизить единственного менеджера проекта. Сначала назначьте менеджером другого участника.",
    );
    expect(memberErrorText(last, "remove")).toMatch(/^Нельзя исключить единственного менеджера/);
  });

  it("деактивация — со списком проектов из details", () => {
    const e = apiError(409, "LAST_MANAGER", "", { projects: ["TM", "OPS"] });
    expect(memberErrorText(e, "deactivate")).toBe(
      "Нельзя деактивировать: это единственный менеджер проекта (TM, OPS). Сначала назначьте менеджером другого участника.",
    );
    expect(memberErrorText(last, "deactivate")).toMatch(/единственный менеджер проекта\. Сначала/);
  });

  it("другие ошибки — сообщение сервера", () => {
    expect(memberErrorText(apiError(403, "FORBIDDEN", "Нет прав"), "role")).toBe("Нет прав");
    expect(memberErrorText(new TypeError("fetch"), "remove")).toBe("Нет связи с сервером, проверьте подключение");
  });
});

describe("события бота §8.6", () => {
  it("умолчания из таблицы, сохранённое значение важнее", () => {
    const e = effectiveEvents(undefined);
    expect(Object.keys(e)).toHaveLength(11);
    expect(e.task_created).toBe(true);
    expect(e.daily_summary).toBe(false);
    expect(e.comment_created).toBe(false);
    expect(effectiveEvents({ daily_summary: true, task_created: false })).toMatchObject({
      daily_summary: true,
      task_created: false,
      task_taken: true,
    });
  });

  it("в PATCH уходят только изменённые", () => {
    const before = effectiveEvents(undefined);
    expect(changedEvents(before, { ...before })).toEqual({});
    expect(changedEvents(before, { ...before, daily_summary: true, task_review: false })).toEqual({
      daily_summary: true,
      task_review: false,
    });
  });

  it("время сводки", () => {
    expect(normalizeTime("10:00:00")).toBe("10:00");
    expect(normalizeTime("9:30")).toBe("09:30");
    expect(normalizeTime(undefined)).toBe("");
    expect(isValidTime("23:59")).toBe(true);
    expect(isValidTime("24:00")).toBe(false);
    expect(isValidTime("9:00")).toBe(false);
  });
});

describe("журнал аудита §3.8", () => {
  it("подпись действия, неизвестное — как есть", () => {
    expect(auditActionLabel("member.role_changed")).toBe("Роль изменена");
    expect(auditActionLabel("x.y")).toBe("x.y");
  });

  it("изменения: было → стало", () => {
    expect(
      auditChanges({ old_value: { role: "manager", name: "A" }, new_value: { role: "member", is_active: false } }),
    ).toEqual(["role: manager → member", "name: A → —", "is_active: нет"]);
    expect(auditChanges({})).toEqual([]);
  });

  it("над чем действие", () => {
    const nameOf = (id: string) => (id === "u1" ? "Иван" : id);
    expect(auditEntity({ entity_type: "user", entity_id: "u1", new_value: { role: "member" } }, nameOf)).toBe("Иван");
    expect(auditEntity({ entity_type: "task", entity_id: "t1", new_value: { key: "TM-5" } }, nameOf)).toBe("TM-5");
    expect(auditEntity({ entity_type: "task", entity_id: "t1" }, nameOf)).toBe("t1");
    expect(
      auditEntity({ entity_type: "worklog", entity_id: "w1", old_value: { task_key: "TM-7", user_id: "u1" } }, nameOf),
    ).toBe("TM-7, Иван");
    expect(auditEntity({ entity_type: "project", entity_id: "p1" }, nameOf)).toBe("");
  });

  it("фильтр не предлагает действий без проекта (user.*)", () => {
    expect(AUDIT_ACTIONS.some((a) => a.value.startsWith("user."))).toBe(false);
  });

  it("пустые фильтры не передаются", () => {
    expect(auditQuery({ from: "", to: "", userId: "", action: "" })).toEqual({});
    expect(auditQuery({ from: "2026-10-01", to: "2026-10-11", userId: "u1", action: "task.deleted" })).toEqual({
      from: "2026-10-01",
      to: "2026-10-11",
      user_id: "u1",
      action: "task.deleted",
    });
  });
});

describe("корзина", () => {
  it("кто удалил — имя из ответа сервера, неизвестен — прочерк", () => {
    expect(trashDeleter({ deleted_by: { id: "u1", display_name: "Админ" } })).toBe("Админ");
    expect(trashDeleter({ deleted_by: undefined })).toBe("—");
    expect(trashDeleter({})).toBe("—");
  });
});
