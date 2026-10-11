import { describe, expect, it } from "vitest";
import type { TaskTask, TaskWorklogView } from "../api/client";
import type { Actor } from "../board/transitions";
import { canAddWorklog, dayOf, validateDraft, worklogErrorText, worklogPerms } from "./worklog";

const manager: Actor = { userId: "m", manager: true, member: true };
const member: Actor = { userId: "u", manager: false, member: true };
const stranger: Actor = { userId: "x", manager: false, member: true };

const task = (t: Partial<TaskTask>): TaskTask => ({ status: "in_progress", reporter_id: "r", ...t });

/** Ответ сгенерированного клиента на ошибку: Response с телом в поле error. */
function apiError(status: number, error: object): Response {
  return Object.assign(new Response(null, { status }), { error: { error } });
}

describe("canAddWorklog", () => {
  it("исполнитель и менеджер списывают, остальные — нет (§2)", () => {
    const t = task({ assignee_id: "u" });
    expect(canAddWorklog(t, member, false)).toBe(true);
    expect(canAddWorklog(t, manager, false)).toBe(true);
    expect(canAddWorklog(t, stranger, false)).toBe(false);
    expect(canAddWorklog(task({}), member, false)).toBe(false);
    expect(canAddWorklog(t, null, false)).toBe(false);
  });

  it("архив, удалённая и отменённая задача — нельзя", () => {
    expect(canAddWorklog(task({ assignee_id: "u" }), manager, true)).toBe(false);
    expect(canAddWorklog(task({ status: "cancelled" }), manager, false)).toBe(false);
  });

  it("в любом другом статусе можно, даже в «Готово»", () => {
    for (const status of ["todo", "review", "done"] as const)
      expect(canAddWorklog(task({ status, assignee_id: "u" }), member, false)).toBe(true);
  });
});

describe("worklogPerms (§6.5)", () => {
  const w = (p: Partial<TaskWorklogView>): TaskWorklogView => ({ user_id: "u", work_date: "2026-10-04", ...p });
  const opts = { readOnly: false, cancelled: false, today: "2026-10-11" };

  it("автор — 7 календарных дней после work_date включительно", () => {
    expect(worklogPerms(w({}), member, opts)).toEqual({ edit: true, remove: true });
    expect(worklogPerms(w({ work_date: "2026-10-03" }), member, opts)).toEqual({ edit: false, remove: false });
  });

  it("чужую запись — только менеджер, без срока", () => {
    expect(worklogPerms(w({ user_id: "x" }), member, opts)).toEqual({ edit: false, remove: false });
    expect(worklogPerms(w({ user_id: "x", work_date: "2025-01-01" }), manager, opts)).toEqual({ edit: true, remove: true });
  });

  it("отменённая задача — только удаление; архив — ничего", () => {
    expect(worklogPerms(w({}), member, { ...opts, cancelled: true })).toEqual({ edit: false, remove: true });
    expect(worklogPerms(w({}), manager, { ...opts, readOnly: true })).toEqual({ edit: false, remove: false });
  });
});

describe("validateDraft", () => {
  const range = { today: "2026-10-11", minDate: "2026-10-01" };
  const ok = { duration: "2h 30m", workDate: "2026-10-11", comment: "" };

  it("корректная форма", () => {
    expect(validateDraft(ok, range)).toEqual({});
    expect(validateDraft({ ...ok, duration: "1d" }, range)).toEqual({});
    expect(validateDraft({ ...ok, duration: "24h" }, range)).toEqual({});
  });

  it.each([
    ["", "Укажите длительность"],
    ["2 часа", "Формат: 2h 30m, 45m, 3h, 1d (1d = 8h)"],
    ["30m 2h", "Формат: 2h 30m, 45m, 3h, 1d (1d = 8h)"],
    ["0m", "От 1m до 24h"],
    ["24h 1m", "От 1m до 24h"],
  ])("длительность %j → %s", (duration, want) => {
    expect(validateDraft({ ...ok, duration }, range).duration).toBe(want);
  });

  it("дата: не позже сегодня и не раньше минимальной", () => {
    expect(validateDraft({ ...ok, workDate: "2026-10-12" }, range).workDate).toBe("Не позже сегодняшней даты");
    expect(validateDraft({ ...ok, workDate: "2026-09-30" }, range).workDate).toBe("Не раньше 01.10.2026");
    expect(validateDraft({ ...ok, workDate: "" }, range).workDate).toBe("Укажите дату");
  });

  it("неизменённую дату записи при правке не проверяем — её день по поясу автора", () => {
    const keep = { ...range, keepDate: "2026-09-30" };
    expect(validateDraft({ ...ok, workDate: "2026-09-30" }, keep)).toEqual({});
    expect(validateDraft({ ...ok, workDate: "2026-09-29" }, keep).workDate).toBe("Не раньше 01.10.2026");
  });

  it("комментарий до 500 символов (эмодзи — один символ)", () => {
    expect(validateDraft({ ...ok, comment: "😀".repeat(500) }, range)).toEqual({});
    expect(validateDraft({ ...ok, comment: "я".repeat(500) }, range)).toEqual({});
    expect(validateDraft({ ...ok, comment: "я".repeat(501) }, range).comment).toBe("Не длиннее 500 символов");
  });
});

describe("worklogErrorText", () => {
  it("дневной лимит — со списанным и доступным", () => {
    const e = apiError(409, {
      code: "DAILY_LIMIT_EXCEEDED",
      message: "За один день можно списать не больше 24 часов",
      details: { used_minutes: 1290, available_minutes: 150 },
    });
    expect(worklogErrorText(e)).toBe("За один день можно списать не больше 24 часов, уже списано 21h 30m, доступно 2h 30m");
  });

  it("дневной лимит без чисел в details — без «0m»", () => {
    const e = apiError(409, {
      code: "DAILY_LIMIT_EXCEEDED",
      message: "",
      details: { used_minutes: null },
    });
    expect(worklogErrorText(e)).toBe("За один день можно списать не больше 24 часов");
  });

  it("ошибка поля — с причиной", () => {
    const e = apiError(400, {
      code: "VALIDATION_FAILED",
      message: "Некорректные данные запроса",
      details: { fields: [{ field: "work_date", reason: "не раньше даты создания задачи" }] },
    });
    expect(worklogErrorText(e)).toBe("Дата: не раньше даты создания задачи");
  });

  it("остальное — сообщение сервера", () => {
    const e = apiError(409, { code: "INVALID_TRANSITION", message: "Задача в завершённом спринте", details: {} });
    expect(worklogErrorText(e)).toBe("Задача в завершённом спринте");
    expect(worklogErrorText(new TypeError("fetch"))).toBe("Нет связи с сервером, проверьте подключение");
  });
});

describe("dayOf", () => {
  it("дата в поясе пользователя", () => {
    expect(dayOf("2026-10-10T20:30:00Z", "Asia/Tashkent")).toBe("2026-10-11");
    expect(dayOf("2026-10-10T20:30:00Z", "UTC")).toBe("2026-10-10");
    expect(dayOf(undefined)).toBe("");
  });
});
