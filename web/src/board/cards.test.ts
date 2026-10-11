import { describe, expect, it } from "vitest";
import { applyMove, groupByStatus, initials } from "./cards";

describe("initials", () => {
  it.each([
    ["Иван Петров", "ИП"],
    ["анна", "А"],
    ["  Мария  Сергеевна Иванова ", "МС"],
    ["", ""],
    [undefined, ""],
  ])("%s → %s", (name, want) => {
    expect(initials(name)).toBe(want);
  });
});

describe("groupByStatus", () => {
  it("четыре колонки, отменённые не показываются, порядок сохраняется", () => {
    const g = groupByStatus([
      { id: "1", status: "todo" },
      { id: "2", status: "cancelled" },
      { id: "3", status: "done" },
      { id: "4", status: "todo" },
    ]);
    expect([...g.keys()]).toEqual(["todo", "in_progress", "review", "done"]);
    expect(g.get("todo")!.map((t) => t.id)).toEqual(["1", "4"]);
    expect(g.get("in_progress")).toEqual([]);
    expect(g.get("done")!.map((t) => t.id)).toEqual(["3"]);
  });
});

describe("applyMove", () => {
  it("меняет статус, остальное как было", () => {
    expect(applyMove({ id: "1", status: "in_progress", assignee_id: "u" }, "review", "me")).toEqual({
      id: "1",
      status: "review",
      assignee_id: "u",
    });
  });
  it("отказаться — исполнитель очищается", () => {
    expect(applyMove({ status: "in_progress", assignee_id: "u" }, "todo", "me").assignee_id).toBeUndefined();
  });
  it("взять свободную — исполнителем становится тот, кто взял; назначенную — не меняется", () => {
    expect(applyMove({ status: "todo" }, "in_progress", "me").assignee_id).toBe("me");
    expect(applyMove({ status: "todo", assignee_id: "u" }, "in_progress", "me").assignee_id).toBe("u");
  });
});
