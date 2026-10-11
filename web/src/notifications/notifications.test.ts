import { describe, expect, it } from "vitest";
import { badgeText, describeNotification, markReadInCache, snippet, type NotificationsData } from "./notifications";

describe("describeNotification", () => {
  it("комментарий: ключ, название, начало текста, ссылка на задачу", () => {
    expect(
      describeNotification({ type: "comment", payload: { key: "TM-42", title: "Отчёт", body: "Готово,\n\nсмотрите" } }),
    ).toEqual({ text: "Новый комментарий к задаче TM-42 «Отчёт»", detail: "Готово, смотрите", link: "/tasks/TM-42" });
  });

  it("удаление задачи — без ссылки", () => {
    expect(describeNotification({ type: "task_deleted", payload: { key: "TM-1", title: "Старое" } })).toEqual({
      text: "Задача TM-1 «Старое» удалена",
    });
  });

  it("добавление в проект — с ролью и ссылкой на проект, исключение — без ссылки", () => {
    expect(describeNotification({ type: "member_added", payload: { project_key: "TM", role: "manager" } })).toEqual({
      text: "Вас добавили в проект TM с ролью «менеджер»",
      link: "/projects/TM",
    });
    expect(describeNotification({ type: "member_added", payload: { project_key: "TM", role: "member" } }).text).toBe(
      "Вас добавили в проект TM с ролью «участник»",
    );
    expect(describeNotification({ type: "member_removed", payload: { project_key: "TM" } })).toEqual({
      text: "Вас исключили из проекта TM",
    });
  });

  it("автоостановка таймера", () => {
    expect(
      describeNotification({ type: "timer_auto_stopped", payload: { key: "TM-5", title: "Долгая", minutes: 720 } }),
    ).toEqual({
      text: "Таймер на задаче TM-5 «Долгая» остановлен автоматически через 12 часов",
      detail: "Списано 12h",
      link: "/tasks/TM-5",
    });
    expect(describeNotification({ type: "timer_auto_stopped", payload: { key: "TM-5", minutes: 0 } }).detail).toBe(
      "Время не списано",
    );
  });

  it("обрезка по дневному лимиту: частично и полностью", () => {
    expect(
      describeNotification({
        type: "worklog_trimmed",
        payload: { key: "TM-7", work_date: "2026-10-10", requested_minutes: 90, written_minutes: 30 },
      }),
    ).toEqual({
      text: "Запись таймера по задаче TM-7 обрезана по дневному лимиту 24h",
      detail: "За 10.10.2026: списано 30m из 1h 30m",
      link: "/tasks/TM-7",
    });
    expect(
      describeNotification({
        type: "worklog_trimmed",
        payload: { key: "TM-7", work_date: "2026-10-10", requested_minutes: 45, written_minutes: 0 },
      }),
    ).toEqual({
      text: "Время таймера по задаче TM-7 не списано: дневной лимит 24h исчерпан",
      detail: "За 10.10.2026: не записано 45m",
      link: "/tasks/TM-7",
    });
  });

  it("неизвестный тип и пустой payload не роняют список", () => {
    expect(describeNotification({ type: "something_new", payload: { key: "TM-3" } })).toEqual({
      text: "Событие по задаче TM-3",
      link: "/tasks/TM-3",
    });
    expect(describeNotification({})).toEqual({ text: "Новое событие", link: undefined });
    expect(describeNotification({ type: "comment", payload: { key: "TM-3", body: 5 } }).detail).toBeUndefined();
  });
});

it("snippet обрезает по символам, а не по байтам", () => {
  expect(snippet("а".repeat(10), 5)).toBe("аааа…");
  expect(snippet("  коротко  ")).toBe("коротко");
});

describe("markReadInCache", () => {
  const data = (): NotificationsData => ({
    pageParams: ["", "c1"],
    pages: [
      {
        unread_count: 3,
        next_cursor: "c1",
        items: [
          { id: "a", read_at: undefined },
          { id: "b", read_at: "2026-10-01T00:00:00Z" },
        ],
      },
      { items: [{ id: "c" }, { id: "d" }] },
    ],
  });

  it("одно уведомление: отмечено, счётчик уменьшен", () => {
    const next = markReadInCache(data(), ["c"], "now")!;
    expect(next.pages[1]!.items!.map((n) => n.read_at)).toEqual(["now", undefined]);
    expect(next.pages[0]!.unread_count).toBe(2);
  });

  it("уже прочитанное не уменьшает счётчик", () => {
    const next = markReadInCache(data(), ["b"], "now")!;
    expect(next.pages[0]!.items![1]!.read_at).toBe("2026-10-01T00:00:00Z");
    expect(next.pages[0]!.unread_count).toBe(3);
  });

  it("все: счётчик в ноль, в том числе за ещё не загруженные страницы", () => {
    const next = markReadInCache(data(), null, "now")!;
    expect(next.pages.flatMap((p) => p.items!.map((n) => !!n.read_at))).toEqual([true, true, true, true]);
    expect(next.pages[0]!.unread_count).toBe(0);
  });

  it("исходные данные не меняются, пустой кэш остаётся пустым", () => {
    const d = data();
    markReadInCache(d, null, "now");
    expect(d.pages[0]!.unread_count).toBe(3);
    expect(d.pages[0]!.items![0]!.read_at).toBeUndefined();
    expect(markReadInCache(undefined, null, "now")).toBeUndefined();
  });
});

it("badgeText", () => {
  expect(badgeText(undefined)).toBe("");
  expect(badgeText(0)).toBe("");
  expect(badgeText(7)).toBe("7");
  expect(badgeText(100)).toBe("99+");
});
