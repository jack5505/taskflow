import { describe, expect, it } from "vitest";
import type { TaskTask } from "../api/client";
import { buildPatch, compareConflict, ifMatch, isEmptyPatch, parseLabels, rebasePatch, versionConflict } from "./patch";

const base: TaskTask = {
  key: "TM-1",
  version: 3,
  title: "Вход",
  description: "Текст",
  type: "task",
  priority: "medium",
  status: "todo",
  assignee_id: "u1",
  due_date: "2026-10-31",
  estimate_minutes: 120,
  labels: ["api"],
};

/** Ответ клиента на 409: сгенерированный клиент бросает Response с полем error. */
function conflictResponse(current: unknown, code = "VERSION_CONFLICT", status = 409) {
  const r = new Response(null, { status });
  return Object.assign(r, { error: { error: { code, message: "Задачу уже изменил другой пользователь", details: { current } } } });
}

describe("ifMatch", () => {
  it("версия в кавычках — сильный ETag", () => {
    expect(ifMatch(5)).toEqual({ "If-Match": '"5"' });
  });
});

describe("buildPatch", () => {
  it("только изменённые поля", () => {
    expect(buildPatch(base, { title: "Вход по паролю", priority: "medium", labels: ["api"] })).toEqual({
      title: "Вход по паролю",
    });
  });

  it("очищенное поле — null, метки — пустой массив", () => {
    expect(buildPatch(base, { due_date: "", estimate_minutes: undefined, assignee_id: undefined, labels: [] })).toEqual({
      due_date: null,
      estimate_minutes: null,
      assignee_id: null,
      labels: [],
    });
  });

  it("описание из пробелов — очистка; пробелы внутри описания сохраняются", () => {
    expect(buildPatch(base, { description: "   " })).toEqual({ description: null });
    expect(buildPatch(base, { description: "  отступ\n" })).toEqual({ description: "  отступ\n" });
  });

  it("название обрезается, без изменений — пустой PATCH", () => {
    expect(buildPatch(base, { title: "  Вход " })).toEqual({});
    expect(isEmptyPatch(buildPatch(base, {}))).toBe(true);
  });

  it("новое значение в пустом поле", () => {
    const t = { ...base, estimate_minutes: undefined, labels: undefined };
    expect(buildPatch(t, { estimate_minutes: 30, labels: ["ui", "api"] })).toEqual({
      estimate_minutes: 30,
      labels: ["ui", "api"],
    });
  });

  it("порядок меток важен", () => {
    const t = { ...base, labels: ["a", "b"] };
    expect(buildPatch(t, { labels: ["b", "a"] })).toEqual({ labels: ["b", "a"] });
  });
});

describe("versionConflict", () => {
  it("актуальная задача из details.current", () => {
    const cur = { ...base, version: 4, title: "Чужое" };
    expect(versionConflict(conflictResponse(cur))).toEqual(cur);
  });

  it("другие ошибки — null", () => {
    expect(versionConflict(conflictResponse(base, "PROJECT_ARCHIVED"))).toBeNull();
    expect(versionConflict(conflictResponse(base, "VERSION_CONFLICT", 400))).toBeNull();
    expect(versionConflict(conflictResponse(undefined))).toBeNull();
    expect(versionConflict(new TypeError("сеть"))).toBeNull();
  });
});

describe("compareConflict", () => {
  it("чужие правки и свои, совпадение полей — конфликт", () => {
    const current = { ...base, version: 4, title: "Вход (чужое)", status: "in_progress" as const };
    const rows = compareConflict(base, current, { title: "Вход (моё)", priority: "high" });
    expect(rows).toEqual([
      {
        field: "title",
        before: "Вход",
        theirs: "Вход (чужое)",
        changedByOthers: true,
        mine: "Вход (моё)",
        inMine: true,
        clash: true,
      },
      {
        field: "status",
        before: "todo",
        theirs: "in_progress",
        changedByOthers: true,
        mine: undefined,
        inMine: false,
        clash: false,
      },
      {
        field: "priority",
        before: "medium",
        theirs: "medium",
        changedByOthers: false,
        mine: "high",
        inMine: true,
        clash: false,
      },
    ]);
  });

  it("одинаковое значение у обоих — не конфликт", () => {
    const current = { ...base, version: 4, priority: "high" as const };
    const [row] = compareConflict(base, current, { priority: "high" });
    expect(row).toMatchObject({ field: "priority", changedByOthers: true, clash: false });
  });

  it("очистка поля другим пользователем видна", () => {
    const current = { ...base, version: 4, due_date: undefined, labels: [] };
    expect(compareConflict(base, current, {}).map((r) => r.field)).toEqual(["due_date", "labels"]);
  });
});

describe("rebasePatch", () => {
  it("свои поля поверх актуальной версии; уже совпавшие отпадают", () => {
    const current = { ...base, version: 4, priority: "high" as const, title: "Чужое" };
    expect(rebasePatch(current, { title: "Моё", priority: "high", due_date: null })).toEqual({
      title: "Моё",
      due_date: null,
    });
    expect(isEmptyPatch(rebasePatch(current, { priority: "high" }))).toBe(true);
  });
});

describe("parseLabels", () => {
  it("через запятую, без пустых и повторов", () => {
    expect(parseLabels(" api, вход ,, api ,ui ")).toEqual(["api", "вход", "ui"]);
    expect(parseLabels("  ")).toEqual([]);
  });
});
