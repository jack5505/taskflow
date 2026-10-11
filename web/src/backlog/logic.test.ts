import { describe, expect, it } from "vitest";
import type { SprintView, TaskListItem } from "../api/client";
import {
  addDays,
  applyMove,
  archiveSummary,
  BACKLOG,
  buildSections,
  closeSummary,
  daysBetween,
  dropTarget,
  sprintDatesError,
  sprintErrorText,
  totals,
  type Section,
} from "./logic";

const sprints: SprintView[] = [
  { id: "p1", name: "Спринт 2", state: "planned" },
  { id: "c1", name: "Спринт 0", state: "closed" },
  { id: "a1", name: "Спринт 1", state: "active" },
  { id: "p2", name: "Спринт 3", state: "planned" },
];

const task = (id: string, sprint?: string, extra: Partial<TaskListItem> = {}): TaskListItem => ({
  id,
  key: `TM-${id}`,
  sprint_id: sprint,
  ...extra,
});

describe("buildSections", () => {
  it("активный, запланированные по порядку сервера, бэклог; закрытые и их задачи отброшены", () => {
    const s = buildSections(sprints, [task("1"), task("2", "a1"), task("3", "p2"), task("4", "c1"), task("5"), task("6", "a1")]);
    expect(s.map((x) => x.id)).toEqual(["a1", "p1", "p2", BACKLOG]);
    expect(s[0]!.tasks.map((t) => t.id)).toEqual(["2", "6"]);
    expect(s[1]!.tasks).toEqual([]);
    expect(s[2]!.tasks.map((t) => t.id)).toEqual(["3"]);
    expect(s[3]!.tasks.map((t) => t.id)).toEqual(["1", "5"]);
  });
});

describe("totals", () => {
  it("число задач, сумма оценок, без оценки", () => {
    expect(totals([task("1", undefined, { estimate_minutes: 90 }), task("2"), task("3", undefined, { estimate_minutes: 30 })])).toEqual({
      count: 3,
      estimate: 120,
      withoutEstimate: 1,
    });
    expect(totals([])).toEqual({ count: 0, estimate: 0, withoutEstimate: 0 });
  });
});

describe("dropTarget", () => {
  // Спринт s: A B C; бэклог: D E.
  const sections: Section[] = [
    { id: "s", tasks: [task("A", "s"), task("B", "s"), task("C", "s")] },
    { id: BACKLOG, tasks: [task("D"), task("E")] },
    { id: "empty", tasks: [] },
  ];
  it.each([
    ["вниз по секции — после строки", "A", { kind: "row", taskId: "C" }, { sectionId: "s", afterTaskId: "C", crossSection: false }],
    ["вверх по секции — перед строкой", "C", { kind: "row", taskId: "A" }, { sectionId: "s", afterTaskId: null, crossSection: false }],
    ["вверх на середину", "C", { kind: "row", taskId: "B" }, { sectionId: "s", afterTaskId: "A", crossSection: false }],
    ["вниз на соседнюю", "A", { kind: "row", taskId: "B" }, { sectionId: "s", afterTaskId: "B", crossSection: false }],
    ["в конец своей секции", "A", { kind: "section", sectionId: "s" }, { sectionId: "s", afterTaskId: "C", crossSection: false }],
    ["из бэклога на строку спринта — перед ней", "D", { kind: "row", taskId: "B" }, { sectionId: "s", afterTaskId: "A", crossSection: true }],
    ["на первую строку чужой секции — в начало", "D", { kind: "row", taskId: "A" }, { sectionId: "s", afterTaskId: null, crossSection: true }],
    ["в конец чужой секции", "B", { kind: "section", sectionId: BACKLOG }, { sectionId: BACKLOG, afterTaskId: "E", crossSection: true }],
    ["в пустую секцию", "B", { kind: "section", sectionId: "empty" }, { sectionId: "empty", afterTaskId: null, crossSection: true }],
  ] as const)("%s", (_name, id, over, want) => {
    expect(dropTarget(sections, id, over)).toEqual(want);
  });

  it.each([
    ["на себя", "B", { kind: "row", taskId: "B" }],
    ["в конец секции, где и так последняя", "C", { kind: "section", sectionId: "s" }],
    ["неизвестная задача", "X", { kind: "row", taskId: "A" }],
  ] as const)("без изменений: %s", (_name, id, over) => {
    expect(dropTarget(sections, id, over)).toBeNull();
  });
});

describe("applyMove", () => {
  const list = [task("A", "s"), task("D"), task("B", "s"), task("E")];
  const ids = (l: TaskListItem[]) => l.map((t) => `${t.id}:${t.sprint_id ?? "-"}`);
  it("после указанной задачи, со сменой секции", () => {
    expect(ids(applyMove(list, "D", { sectionId: "s", afterTaskId: "B", crossSection: true }))).toEqual(["A:s", "B:s", "D:s", "E:-"]);
  });
  it("в начало секции — перед её первой задачей", () => {
    expect(ids(applyMove(list, "B", { sectionId: "s", afterTaskId: null, crossSection: false }))).toEqual(["B:s", "A:s", "D:-", "E:-"]);
  });
  it("в бэклог — без спринта", () => {
    expect(ids(applyMove(list, "A", { sectionId: BACKLOG, afterTaskId: "E", crossSection: true }))).toEqual(["D:-", "B:s", "E:-", "A:-"]);
  });
  it("в пустую секцию — в конец списка", () => {
    expect(ids(applyMove(list, "D", { sectionId: "z", afterTaskId: null, crossSection: true }))).toEqual(["A:s", "B:s", "E:-", "D:z"]);
  });
  it("неизвестная задача — список без изменений", () => {
    expect(applyMove(list, "X", { sectionId: "s", afterTaskId: null, crossSection: false })).toBe(list);
  });
});

describe("closeSummary и archiveSummary", () => {
  const list = [
    task("1", "s", { status: "done", estimate_minutes: 60, spent_minutes: 50 }),
    task("2", "s", { status: "in_progress", estimate_minutes: 30, spent_minutes: 10 }),
    task("3", "s", { status: "review" }),
    task("4", "s", { status: "todo" }),
    task("5", "s", { status: "cancelled", spent_minutes: 5 }),
  ];
  it("незакрытые — todo, in_progress, review (§5.4)", () => {
    expect(closeSummary(list)).toEqual({ done: 1, open: 3, cancelled: 1 });
  });
  it("сводка архива", () => {
    expect(archiveSummary(list)).toEqual({ done: 1, cancelled: 1, doneEstimate: 60, spent: 65 });
  });
});

describe("даты спринта (§5.2)", () => {
  it("daysBetween", () => {
    expect(daysBetween("2026-10-12", "2026-10-26")).toBe(14);
    expect(daysBetween("2026-03-28", "2026-03-30")).toBe(2); // переход на летнее время не мешает
    expect(daysBetween("2026-10-12", "")).toBeNaN();
  });
  it.each([
    ["", "", false, ""],
    ["", "", true, "Для запуска нужны даты начала и окончания"],
    ["2026-10-12", "", true, "Для запуска нужны даты начала и окончания"],
    ["2026-10-12", "2026-10-12", true, "Окончание — позже начала"],
    ["2026-10-12", "2026-10-11", false, "Окончание — позже начала"],
    ["2026-10-12", "2026-10-13", true, ""],
    ["2026-10-12", "2026-11-23", true, ""],
    ["2026-10-12", "2026-11-24", true, "Спринт не длиннее 42 дней"],
  ])("%s — %s (запуск: %s) → «%s»", (start, end, required, want) => {
    expect(sprintDatesError(start, end, required)).toBe(want);
  });
  it("addDays: спринт на 2 недели", () => {
    expect(addDays("2026-10-12", 14)).toBe("2026-10-26");
    expect(addDays("2026-12-25", 14)).toBe("2027-01-08");
  });
});

describe("sprintErrorText", () => {
  const apiError = (status: number, error: object) => Object.assign(new Response(null, { status }), { error: { error } });
  it("409: сообщение сервера", () => {
    expect(sprintErrorText(apiError(409, { code: "SPRINT_ALREADY_ACTIVE", message: "В проекте уже есть активный спринт" }))).toBe(
      "В проекте уже есть активный спринт",
    );
  });
  it("400: с причинами по полям", () => {
    const e = apiError(400, {
      code: "VALIDATION_FAILED",
      message: "Ошибка валидации",
      details: { fields: [{ field: "end_date", reason: "позже даты начала" }] },
    });
    expect(sprintErrorText(e)).toBe("Ошибка валидации: Окончание — позже даты начала");
  });
  it("нет связи", () => {
    expect(sprintErrorText(new TypeError("fetch failed"))).toBe("Нет связи с сервером, проверьте подключение");
  });
});
