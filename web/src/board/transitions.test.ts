import { describe, expect, it } from "vitest";
import {
  canTransition,
  commentError,
  commentRequirement,
  STATUSES,
  transitionLabel,
  isValidTransition,
  needsNewAssignee,
  type Actor,
  type Status,
  type TaskRef,
} from "./transitions";

const ME = "u-me";
const OTHER = "u-other";

const member: Actor = { userId: ME, manager: false, member: true };
const manager: Actor = { userId: ME, manager: true, member: true };
// Администратор системы без роли в проекте: права менеджера, но исполнителем стать не может.
const adminOutsider: Actor = { userId: ME, manager: true, member: false };

const all: Status[] = ["todo", "in_progress", "review", "done", "cancelled"];

describe("схема переходов §4", () => {
  const valid = new Set([
    "todo>in_progress",
    "todo>cancelled",
    "in_progress>todo",
    "in_progress>review",
    "in_progress>cancelled",
    "review>in_progress",
    "review>done",
    "review>cancelled",
    "done>in_progress",
    "cancelled>todo",
  ]);
  for (const from of all) {
    for (const to of all) {
      const want = valid.has(`${from}>${to}`);
      it(`${from} → ${to}: ${want ? "есть" : "нет"}`, () => {
        expect(isValidTransition(from, to)).toBe(want);
        // Недопустимый переход недоступен никому, даже менеджеру.
        if (!want) expect(canTransition({ status: from, assigneeId: ME, reporterId: ME }, to, manager)).toBe(false);
      });
    }
  }
});

describe("права на переход", () => {
  // [название, задача, куда, кто, ожидание]
  const cases: [string, TaskRef, Status, Actor, boolean][] = [
    // todo → in_progress
    ["свободную берёт любой участник", { status: "todo" }, "in_progress", member, true],
    ["свою берёт исполнитель", { status: "todo", assigneeId: ME }, "in_progress", member, true],
    ["чужую участник взять не может", { status: "todo", assigneeId: OTHER }, "in_progress", member, false],
    ["чужую переводит менеджер", { status: "todo", assigneeId: OTHER }, "in_progress", manager, true],
    ["свободную — не участник проекта нет", { status: "todo" }, "in_progress", adminOutsider, false],
    ["назначенную — администратор как менеджер", { status: "todo", assigneeId: OTHER }, "in_progress", adminOutsider, true],
    // in_progress → todo, review
    ["отказаться — исполнитель", { status: "in_progress", assigneeId: ME }, "todo", member, true],
    ["отказаться — не исполнитель нет", { status: "in_progress", assigneeId: OTHER, reporterId: ME }, "todo", member, false],
    ["отказаться — менеджер", { status: "in_progress", assigneeId: OTHER }, "todo", manager, true],
    ["на проверку — исполнитель", { status: "in_progress", assigneeId: ME }, "review", member, true],
    ["на проверку — автор не исполнитель нет", { status: "in_progress", assigneeId: OTHER, reporterId: ME }, "review", member, false],
    ["на проверку — менеджер", { status: "in_progress", assigneeId: OTHER }, "review", manager, true],
    // review → in_progress
    ["вернуть на доработку — автор", { status: "review", assigneeId: OTHER, reporterId: ME }, "in_progress", member, true],
    ["вернуть свою — автор-исполнитель", { status: "review", assigneeId: ME, reporterId: ME }, "in_progress", member, true],
    ["вернуть — исполнитель не автор нет", { status: "review", assigneeId: ME, reporterId: OTHER }, "in_progress", member, false],
    ["вернуть — менеджер", { status: "review", assigneeId: OTHER, reporterId: OTHER }, "in_progress", manager, true],
    // review → done
    ["принять — автор не исполнитель", { status: "review", assigneeId: OTHER, reporterId: ME }, "done", member, true],
    ["принять свою — автор-исполнитель нет", { status: "review", assigneeId: ME, reporterId: ME }, "done", member, false],
    ["принять — исполнитель не автор нет", { status: "review", assigneeId: ME, reporterId: OTHER }, "done", member, false],
    ["принять — менеджер", { status: "review", assigneeId: ME, reporterId: ME }, "done", manager, true],
    // done → in_progress
    ["переоткрыть — участник нет", { status: "done", assigneeId: ME, reporterId: ME }, "in_progress", member, false],
    ["переоткрыть — менеджер", { status: "done", assigneeId: OTHER }, "in_progress", manager, true],
    // отмена и возврат в бэклог
    ["отменить — участник нет", { status: "todo", assigneeId: ME, reporterId: ME }, "cancelled", member, false],
    ["отменить — менеджер", { status: "review" }, "cancelled", manager, true],
    ["вернуть в бэклог — участник нет", { status: "cancelled", reporterId: ME }, "todo", member, false],
    ["вернуть в бэклог — менеджер", { status: "cancelled" }, "todo", manager, true],
  ];
  it.each(cases)("%s", (_, task, to, actor, want) => {
    expect(canTransition(task, to, actor)).toBe(want);
  });
});

describe("комментарий к переходу", () => {
  it("какие переходы требуют комментарий", () => {
    expect(commentRequirement("review", "in_progress")).toBe("rework");
    expect(commentRequirement("todo", "cancelled")).toBe("cancel");
    expect(commentRequirement("in_progress", "cancelled")).toBe("cancel");
    expect(commentRequirement("review", "cancelled")).toBe("cancel");
    expect(commentRequirement("todo", "in_progress")).toBeNull();
    expect(commentRequirement("in_progress", "review")).toBeNull();
    expect(commentRequirement("review", "done")).toBeNull();
    expect(commentRequirement("done", "in_progress")).toBeNull();
  });

  it("причина возврата — от 5 символов без пробелов по краям", () => {
    expect(commentError("rework", "")).not.toBe("");
    expect(commentError("rework", "  абв  ")).not.toBe("");
    expect(commentError("rework", "abcd")).not.toBe("");
    expect(commentError("rework", "ошибк")).toBe("");
    expect(commentError("rework", "🙂🙂🙂🙂🙂")).toBe(""); // символы, а не UTF-16
  });

  it("причина отмены — обязательна", () => {
    expect(commentError("cancel", "   ")).not.toBe("");
    expect(commentError("cancel", "x")).toBe("");
  });
});

describe("переоткрытие с деактивированным исполнителем", () => {
  it("спрашиваем нового исполнителя только при done → in_progress и неактивном", () => {
    expect(needsNewAssignee("done", "in_progress", false)).toBe(true);
    expect(needsNewAssignee("done", "in_progress", true)).toBe(false);
    expect(needsNewAssignee("done", "in_progress", undefined)).toBe(false);
    expect(needsNewAssignee("review", "in_progress", false)).toBe(false);
  });
});

describe("transitionLabel", () => {
  it("каждый допустимый переход назван по §4", () => {
    const got: string[] = [];
    for (const from of STATUSES)
      for (const to of STATUSES) if (isValidTransition(from, to)) got.push(`${from}>${to}: ${transitionLabel(from, to)}`);
    expect(got).toEqual([
      "todo>in_progress: Взять в работу",
      "todo>cancelled: Отменить",
      "in_progress>todo: Отказаться",
      "in_progress>review: На проверку",
      "in_progress>cancelled: Отменить",
      "review>in_progress: Вернуть на доработку",
      "review>done: Принять",
      "review>cancelled: Отменить",
      "done>in_progress: Переоткрыть",
      "cancelled>todo: Вернуть в бэклог",
    ]);
  });
});
