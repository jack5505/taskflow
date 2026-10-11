import { describe, expect, it } from "vitest";
import { emptyFilter, filterFromParams, isFilterEmpty, NO_ASSIGNEE, paramsFromFilter, tasksQuery, toggle } from "./filters";

const U1 = "11111111-1111-4111-8111-111111111111";
const U2 = "22222222-2222-4222-8222-222222222222";
const SPRINT = "33333333-3333-4333-8333-333333333333";

describe("фильтры в адресе", () => {
  it("пустой адрес — без фильтров", () => {
    expect(filterFromParams(new URLSearchParams())).toEqual(emptyFilter);
    expect(isFilterEmpty(emptyFilter)).toBe(true);
  });

  it("переживают перезагрузку: фильтр → адрес → фильтр", () => {
    const f = {
      assignees: [U1, NO_ASSIGNEE],
      mine: true,
      types: ["bug" as const, "feature" as const],
      priorities: ["critical" as const],
      label: "api",
      q: "вход TM-4",
    };
    const url = paramsFromFilter(f).toString();
    expect(filterFromParams(new URLSearchParams(url))).toEqual(f);
    expect(isFilterEmpty(f)).toBe(false);
  });

  it("мусор и повторы из адреса отбрасываются", () => {
    const f = filterFromParams(
      new URLSearchParams(`assignee=${U1}&assignee=${U1}&assignee=abc&type=epic&type=bug&priority=urgent&mine=yes`),
    );
    expect(f.assignees).toEqual([U1]);
    expect(f.types).toEqual(["bug"]);
    expect(f.priorities).toEqual([]);
    expect(f.mine).toBe(false);
  });

  it("toggle добавляет и убирает значение", () => {
    expect(toggle([U1], U2)).toEqual([U1, U2]);
    expect(toggle([U1, U2], U1)).toEqual([U2]);
  });
});

describe("запрос задач доски", () => {
  it("активный спринт и статусы колонок, без отменённых", () => {
    expect(tasksQuery(emptyFilter, SPRINT, U1)).toEqual({
      status: ["todo", "in_progress", "review", "done"],
      sprint_id: [SPRINT],
      limit: 200,
    });
  });

  it("«Без исполнителя» — null, остальные фильтры как есть, пробелы обрезаются", () => {
    const q = tasksQuery(
      { assignees: [U2, NO_ASSIGNEE], mine: false, types: ["task"], priorities: ["low", "high"], label: " api ", q: " TM-1 " },
      SPRINT,
      U1,
    );
    expect(q.assignee_id).toEqual([U2, "null"]);
    expect(q.type).toEqual(["task"]);
    expect(q.priority).toEqual(["low", "high"]);
    expect(q.label).toBe("api");
    expect(q.q).toBe("TM-1");
  });

  it("«Только мои» заменяет выбор исполнителей", () => {
    expect(tasksQuery({ ...emptyFilter, mine: true, assignees: [U2] }, SPRINT, U1).assignee_id).toEqual([U1]);
  });
});
