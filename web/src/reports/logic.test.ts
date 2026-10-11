import { describe, expect, it } from "vitest";
import type { MeProfile } from "../api/client";
import {
  csvFileName,
  filterFromParams,
  formatHours,
  paramsFromFilter,
  projectOptions,
  reportErrorText,
  reportQuery,
  unwrapBlobError,
  userLocked,
  weekday,
  withPeriod,
  withProject,
  type ReportFilter,
} from "./logic";

const U1 = "11111111-1111-4111-8111-111111111111";
const U2 = "22222222-2222-4222-8222-222222222222";
const S1 = "33333333-3333-4333-8333-333333333333";

/** Ответ API с ошибкой, как его бросает сгенерированный клиент. */
function apiError(status: number, code: string, message = "", details: Record<string, unknown> = {}) {
  const r = new Response(null, { status }) as Response & { error: unknown };
  r.error = { error: { code, message, details } };
  return r;
}

const empty: ReportFilter = { from: "", to: "", project: "", sprint: "", user: "", view: "user" };

describe("фильтры отчёта в адресе (§6.6)", () => {
  it("читает все поля", () => {
    const f = filterFromParams(
      new URLSearchParams(`from=2026-10-05&to=2026-10-11&project=TM&sprint=${S1}&user=${U1}&view=task`),
    );
    expect(f).toEqual({ from: "2026-10-05", to: "2026-10-11", project: "TM", sprint: S1, user: U1, view: "task" });
  });

  it("мусор отбрасывает, представление по умолчанию — по пользователям", () => {
    const f = filterFromParams(new URLSearchParams("from=05.10.2026&project=tm&user=x&view=chart"));
    expect(f).toEqual(empty);
  });

  it("спринт без проекта не читает", () => {
    expect(filterFromParams(new URLSearchParams(`sprint=${S1}`)).sprint).toBe("");
  });

  it("туда и обратно без потерь; пустое в адрес не попадает", () => {
    const f: ReportFilter = { from: "2026-10-01", to: "2026-10-31", project: "TM", sprint: S1, user: U2, view: "task" };
    expect(filterFromParams(paramsFromFilter(f))).toEqual(f);
    expect(paramsFromFilter(empty).toString()).toBe("");
  });

  it("правка одной границы периода не даёт «с» позже «по»", () => {
    expect(withPeriod(empty, "2026-10-01", "2026-10-11", "from")).toEqual({ ...empty, from: "2026-10-01", to: "2026-10-11" });
    expect(withPeriod(empty, "2026-10-20", "2026-10-11", "from")).toEqual({ ...empty, from: "2026-10-20", to: "2026-10-20" });
    expect(withPeriod(empty, "2026-10-05", "2026-10-01", "to")).toEqual({ ...empty, from: "2026-10-01", to: "2026-10-01" });
    expect(withPeriod(empty, "2026-10-05", "", "from")).toEqual({ ...empty, from: "2026-10-05", to: "" });
  });

  it("смена проекта сбрасывает спринт и пользователя", () => {
    const f: ReportFilter = { ...empty, from: "2026-10-01", project: "TM", sprint: S1, user: U2 };
    expect(withProject(f, "OPS")).toEqual({ ...empty, from: "2026-10-01", project: "OPS" });
  });
});

describe("проекты и блокировка фильтра «пользователь» (§2)", () => {
  const member: MeProfile = {
    id: U1,
    projects: [
      { key: "TM", name: "Задачи", role: "member" },
      { key: "AB", name: "Альфа", role: "manager" },
    ],
  };

  it("проекты участника по ключу; администратору — ещё и чужие", () => {
    expect(projectOptions(member).map((p) => p.key)).toEqual(["AB", "TM"]);
    const all = [{ key: "ZZ", name: "Чужой" }, { key: "TM", name: "Задачи" }];
    expect(projectOptions(member, all).map((p) => p.key)).toEqual(["AB", "TM"]);
    const admin = { ...member, is_system_admin: true };
    expect(projectOptions(admin, all)).toEqual([
      { key: "AB", name: "Альфа", role: "manager" },
      { key: "TM", name: "Задачи", role: "member" },
      { key: "ZZ", name: "Чужой", role: "" },
    ]);
  });

  it("в проекте решает роль в нём", () => {
    const opts = projectOptions(member);
    expect(userLocked(member, opts, "TM")).toBe(true);
    expect(userLocked(member, opts, "AB")).toBe(false);
  });

  it("без проекта: заблокирован, если нигде не менеджер", () => {
    expect(userLocked(member, projectOptions(member), "")).toBe(false);
    const executor: MeProfile = { id: U1, projects: [{ key: "TM", name: "Задачи", role: "member" }] };
    expect(userLocked(executor, projectOptions(executor), "")).toBe(true);
    expect(userLocked({ id: U1, projects: [] }, [], "")).toBe(true);
  });

  it("администратор системы не заблокирован никогда", () => {
    const admin: MeProfile = { id: U1, is_system_admin: true, projects: [{ key: "TM", role: "member" }] };
    expect(userLocked(admin, projectOptions(admin), "TM")).toBe(false);
    expect(userLocked(admin, [], "")).toBe(false);
  });

  it("пока профиль не загружен — не решаем", () => {
    expect(userLocked(undefined, [], "")).toBe(false);
  });
});

describe("запрос отчёта", () => {
  it("пустые поля не отправляет", () => {
    expect(reportQuery(empty, null)).toEqual({
      from: undefined,
      to: undefined,
      project: undefined,
      sprint_id: undefined,
      user_id: undefined,
      group_by: "user",
    });
  });

  it("заблокированный фильтр — всегда сам пользователь, что бы ни было в адресе", () => {
    const f: ReportFilter = { ...empty, project: "TM", sprint: S1, user: U2, view: "task" };
    expect(reportQuery(f, U1)).toEqual({
      from: undefined,
      to: undefined,
      project: "TM",
      sprint_id: S1,
      user_id: U1,
      group_by: "task",
    });
    expect(reportQuery(f, null).user_id).toBe(U2);
  });
});

describe("отображение", () => {
  it("десятичные часы с двумя знаками (§6.1)", () => {
    expect(formatHours(150)).toBe("2.50");
    expect(formatHours(0)).toBe("0.00");
    expect(formatHours(1)).toBe("0.02");
    expect(formatHours(undefined)).toBe("0.00");
  });

  it("день недели для заголовка столбца", () => {
    expect(weekday("2026-10-05")).toBe("пн");
    expect(weekday("2026-10-11")).toBe("вс");
    expect(weekday("мусор")).toBe("");
  });
});

describe("ошибки отчёта (§9.3)", () => {
  it("ошибки полей — по-русски с названием поля", () => {
    const e = apiError(400, "VALIDATION_FAILED", "Некорректные данные запроса", {
      fields: [{ field: "to", reason: "не раньше from" }],
    });
    expect(reportErrorText(e)).toBe("Дата «по»: не раньше from");
  });

  it("403 и 404 — сообщение сервера, без него — понятный текст", () => {
    expect(reportErrorText(apiError(403, "FORBIDDEN", "Отчёт по другим пользователям доступен менеджеру проекта"))).toBe(
      "Отчёт по другим пользователям доступен менеджеру проекта",
    );
    expect(reportErrorText(apiError(403, "FORBIDDEN"))).toBe("Отчёт по другим пользователям доступен менеджеру проекта");
    expect(reportErrorText(apiError(404, "NOT_FOUND"))).toBe("Проект или спринт не найден");
  });

  it("прочее и сеть", () => {
    expect(reportErrorText(apiError(500, "INTERNAL"))).toBe("Не удалось построить отчёт, попробуйте ещё раз");
    expect(reportErrorText(new TypeError("Failed to fetch"))).toBe("Нет связи с сервером, проверьте подключение");
  });

  it("ошибка при скачивании CSV: JSON из Blob превращается в обычную ошибку API", async () => {
    const r = new Response(null, { status: 403 }) as Response & { error: unknown };
    r.error = new Blob([JSON.stringify({ error: { code: "FORBIDDEN", message: "Нельзя", details: {} } })]);
    expect(reportErrorText(await unwrapBlobError(r))).toBe("Нельзя");

    const broken = new Response(null, { status: 502 }) as Response & { error: unknown };
    broken.error = new Blob(["<html>Bad Gateway</html>"]);
    expect(reportErrorText(await unwrapBlobError(broken))).toBe("Не удалось построить отчёт, попробуйте ещё раз");

    const net = new TypeError("Failed to fetch");
    expect(await unwrapBlobError(net)).toBe(net);
  });
});

describe("имя файла CSV", () => {
  const f = { from: "2026-10-05", to: "2026-10-11", view: "user" as const };

  it("из Content-Disposition сервера", () => {
    expect(csvFileName('attachment; filename="time-report-task-2026-10-05_2026-10-11.csv"', f)).toBe(
      "time-report-task-2026-10-05_2026-10-11.csv",
    );
    expect(csvFileName("attachment; filename*=UTF-8''%D0%BE%D1%82%D1%87%D1%91%D1%82.csv", f)).toBe("отчёт.csv");
  });

  it("без заголовка — по представлению и периоду", () => {
    expect(csvFileName(null, f)).toBe("time-report-user-2026-10-05_2026-10-11.csv");
    expect(csvFileName("attachment", { view: "task" })).toBe("time-report-task.csv");
  });
});
