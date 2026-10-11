import { describe, expect, it } from "vitest";
import type { TaskListItem } from "../api/client";
import {
  auditActionLabel,
  auditChanges,
  auditQuery,
  changedEvents,
  effectiveEvents,
  isValidTime,
  memberErrorText,
  normalizeTime,
  tabFromParam,
  trashItems,
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
  const task = (id: string, key: string, deleted_at?: string): TaskListItem => ({ id, key, title: key, deleted_at });

  it("только удалённые, новые сверху, кто удалил — из последней записи журнала", () => {
    const items = trashItems(
      [task("1", "TM-1", "2026-10-01T10:00:00Z"), task("2", "TM-2"), task("3", "TM-3", "2026-10-05T10:00:00Z")],
      [
        { action: "task.deleted", entity_id: "1", actor_id: "old", created_at: "2026-09-01T10:00:00Z" },
        { action: "task.deleted", entity_id: "1", actor_id: "admin", created_at: "2026-10-01T10:00:00Z" },
        { action: "task.restored", entity_id: "3", actor_id: "x", created_at: "2026-10-06T10:00:00Z" },
      ],
    );
    expect(items.map((i) => i.key)).toEqual(["TM-3", "TM-1"]);
    expect(items[1].deletedBy).toBe("admin");
    expect(items[0].deletedBy).toBeUndefined();
  });
});
