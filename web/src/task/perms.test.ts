import { describe, expect, it } from "vitest";
import type { TaskTask } from "../api/client";
import type { Actor } from "../board/transitions";
import { canManageComment, isPreviewable, taskPerms } from "./perms";

const manager: Actor = { userId: "m", manager: true, member: true };
const member: Actor = { userId: "u", manager: false, member: true };
const stranger: Actor = { userId: "x", manager: false, member: true };
const open = { archived: false, admin: false };

const task = (t: Partial<TaskTask>): TaskTask => ({ status: "todo", reporter_id: "r", ...t });

describe("taskPerms", () => {
  it("менеджер: всё, кроме восстановления", () => {
    expect(taskPerms(task({ assignee_id: "u" }), manager, open)).toEqual({
      edit: true,
      assign: true,
      unassign: true,
      estimate: true,
      take: false,
      comment: true,
      remove: true,
      restore: false,
    });
  });

  it("исполнитель правит, но не назначает и не удаляет", () => {
    const p = taskPerms(task({ assignee_id: "u" }), member, open);
    expect(p).toMatchObject({ edit: true, assign: false, remove: false, take: true, estimate: true });
  });

  it("автор правит свою задачу", () => {
    expect(taskPerms(task({ reporter_id: "u" }), member, open).edit).toBe(true);
  });

  it("чужая задача: только комментарии и «Взять», если свободна", () => {
    expect(taskPerms(task({}), stranger, open)).toMatchObject({ edit: false, comment: true, take: true });
    expect(taskPerms(task({ assignee_id: "u" }), stranger, open).take).toBe(false);
  });

  it("оценку задачи, бывшей в работе, меняет только менеджер", () => {
    for (const t of [
      task({ assignee_id: "u", status: "in_progress" }),
      task({ assignee_id: "u", status: "cancelled", started_at: "2026-10-01T10:00:00Z" }),
    ]) {
      expect(taskPerms(t, member, open).estimate).toBe(false);
      expect(taskPerms(t, manager, open).estimate).toBe(true);
    }
  });

  it("исполнителя закрытой задачи не меняют, с задачи в работе не снимают", () => {
    expect(taskPerms(task({ status: "done" }), manager, open).assign).toBe(false);
    const p = taskPerms(task({ status: "in_progress", assignee_id: "u" }), manager, open);
    expect(p.assign).toBe(true);
    expect(p.unassign).toBe(false);
  });

  it("«Взять в работу»: свободная в работе — да, на проверке — нет", () => {
    expect(taskPerms(task({ status: "in_progress" }), member, open).take).toBe(true);
    expect(taskPerms(task({ status: "review" }), member, open).take).toBe(false);
  });

  it("архив и удалённая задача — только чтение; восстановить может администратор", () => {
    expect(Object.values(taskPerms(task({}), manager, { archived: true, admin: true })).some(Boolean)).toBe(false);
    const del = task({ deleted_at: "2026-10-01T10:00:00Z" });
    expect(taskPerms(del, manager, open)).toMatchObject({ edit: false, remove: false, restore: false });
    expect(taskPerms(del, manager, { archived: false, admin: true })).toMatchObject({ edit: false, restore: true });
  });

  it("без профиля — ничего", () => {
    expect(Object.values(taskPerms(task({}), null, open)).some(Boolean)).toBe(false);
  });
});

describe("canManageComment", () => {
  it("автор и менеджер; в архиве никто", () => {
    expect(canManageComment("u", member, false)).toBe(true);
    expect(canManageComment("other", member, false)).toBe(false);
    expect(canManageComment("other", manager, false)).toBe(true);
    expect(canManageComment("u", member, true)).toBe(false);
    expect(canManageComment(undefined, member, false)).toBe(false);
  });
});

describe("isPreviewable", () => {
  it("растровые картинки — превью; SVG и прочее — нет", () => {
    expect(["image/png", "image/jpeg", "image/gif", "image/webp"].every(isPreviewable)).toBe(true);
    expect(["image/svg+xml", "application/pdf", "text/html", "", undefined].some(isPreviewable)).toBe(false);
  });
});
