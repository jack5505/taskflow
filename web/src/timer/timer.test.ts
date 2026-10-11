import { describe, expect, it } from "vitest";
import { canStartTimer, elapsedSeconds, formatClock, projectKeyOf, startMessage, stopMessage } from "./timer";

describe("elapsedSeconds", () => {
  it("досчитывает от ответа сервера по своим часам", () => {
    expect(elapsedSeconds({ elapsed_seconds: 100 }, 1_000_000, 1_000_000)).toBe(100);
    expect(elapsedSeconds({ elapsed_seconds: 100 }, 1_000_000, 1_002_999)).toBe(102);
  });
  it("не уходит в минус и без elapsed_seconds считает с нуля", () => {
    expect(elapsedSeconds({}, 5_000, 1_000)).toBe(0);
    expect(elapsedSeconds({}, 0, 61_000)).toBe(61);
  });
});

describe("formatClock", () => {
  it("Ч:ММ:СС", () => {
    expect(formatClock(0)).toBe("0:00:00");
    expect(formatClock(59)).toBe("0:00:59");
    expect(formatClock(61)).toBe("0:01:01");
    expect(formatClock(3600 * 12)).toBe("12:00:00");
    expect(formatClock(3600 * 2 + 30 * 60 + 5)).toBe("2:30:05");
  });
  it("отрицательное и дробное", () => {
    expect(formatClock(-5)).toBe("0:00:00");
    expect(formatClock(9.9)).toBe("0:00:09");
  });
});

describe("canStartTimer", () => {
  const t = { status: "in_progress", assignee_id: "me" };
  it("исполнитель задачи в работе", () => {
    expect(canStartTimer(t, "me", false)).toBe(true);
  });
  it("не исполнитель, другой статус, архив, удалённая задача — нет", () => {
    expect(canStartTimer(t, "other", false)).toBe(false);
    expect(canStartTimer({ status: "in_progress" }, "me", false)).toBe(false);
    expect(canStartTimer({ ...t, status: "todo" }, "me", false)).toBe(false);
    expect(canStartTimer({ ...t, status: "review" }, "me", false)).toBe(false);
    expect(canStartTimer(t, "me", true)).toBe(false);
    expect(canStartTimer({ ...t, deleted_at: "2026-10-10T10:00:00Z" }, "me", false)).toBe(false);
    expect(canStartTimer(t, undefined, false)).toBe(false);
  });
});

describe("stopMessage", () => {
  it("списанное время в формате §6.1", () => {
    expect(stopMessage({ task_key: "TM-42", minutes: 47, duration: "47m" })).toBe("TM-42: списано 47m");
    expect(stopMessage({ task_key: "TM-42", minutes: 150 })).toBe("TM-42: списано 2h 30m");
  });
  it("меньше минуты — без записи", () => {
    expect(stopMessage({ task_key: "TM-1", minutes: 0, duration: "0m" })).toBe("TM-1: меньше минуты, время не списано");
  });
  it("обрезка по дневному лимиту", () => {
    expect(stopMessage({ task_key: "TM-1", minutes: 30, duration: "30m", trimmed: true })).toBe(
      "TM-1: списано 30m (обрезано по дневному лимиту 24h)",
    );
    expect(stopMessage({ task_key: "TM-1", minutes: 0, trimmed: true })).toBe(
      "TM-1: время не списано — дневной лимит 24h исчерпан",
    );
  });
});

describe("startMessage", () => {
  it("о прежнем таймере — только если он был", () => {
    expect(startMessage({ timer: { task_key: "TM-2" } })).toBe("");
    expect(startMessage({ timer: { task_key: "TM-2" }, stopped: { task_key: "TM-1", minutes: 5, duration: "5m" } })).toBe(
      "Прежний таймер остановлен. TM-1: списано 5m",
    );
  });
});

it("projectKeyOf", () => {
  expect(projectKeyOf("TM-42")).toBe("TM");
  expect(projectKeyOf("AB-CD-7")).toBe("AB-CD");
});
