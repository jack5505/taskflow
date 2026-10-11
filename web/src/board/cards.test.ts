import { describe, expect, it } from "vitest";
import { groupByStatus, initials } from "./cards";

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
