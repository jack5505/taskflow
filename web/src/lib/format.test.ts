import { describe, expect, it } from "vitest";
import { formatDate, formatDateTime, formatTime } from "./format";

describe("formatDate", () => {
  it.each([
    ["2026-10-09T14:30:00Z", "UTC", "09.10.2026"],
    ["2026-10-09T21:30:00Z", "Asia/Tashkent", "10.10.2026"], // +05:00 — уже следующий день
    ["2026-10-09T02:00:00Z", "America/New_York", "08.10.2026"], // -04:00 — ещё предыдущий
    ["2026-01-05T00:00:00+05:00", "Asia/Tashkent", "05.01.2026"],
  ])("момент %s в поясе %s → %s", (value, tz, want) => {
    expect(formatDate(value, tz)).toBe(want);
  });

  it("календарная дата без времени не сдвигается поясом", () => {
    expect(formatDate("2026-10-09", "America/Los_Angeles")).toBe("09.10.2026");
    expect(formatDate("2026-10-09", "Pacific/Kiritimati")).toBe("09.10.2026");
  });

  it("принимает Date", () => {
    expect(formatDate(new Date(Date.UTC(2026, 11, 31, 23, 0)), "UTC")).toBe("31.12.2026");
  });

  it("пустое и некорректное значение — пустая строка", () => {
    expect(formatDate(null)).toBe("");
    expect(formatDate(undefined)).toBe("");
    expect(formatDate("")).toBe("");
    expect(formatDate("не дата")).toBe("");
  });
});

describe("formatTime", () => {
  it.each([
    ["2026-10-09T14:30:00Z", "UTC", "14:30"],
    ["2026-10-09T14:30:00Z", "Asia/Tashkent", "19:30"],
    ["2026-10-09T14:30:00Z", "Europe/Moscow", "17:30"],
    ["2026-10-09T19:05:00Z", "Asia/Tashkent", "00:05"], // полночь — 00, а не 24
  ])("%s в поясе %s → %s", (value, tz, want) => {
    expect(formatTime(value, tz)).toBe(want);
  });

  it("неизвестный пояс не роняет форматирование", () => {
    expect(formatTime("2026-10-09T14:30:00Z", "Нет/Такого")).toMatch(/^\d{2}:\d{2}$/);
  });

  it("некорректное значение — пустая строка", () => {
    expect(formatTime("abc", "UTC")).toBe("");
  });
});

describe("formatDateTime", () => {
  it("дата и время в поясе пользователя", () => {
    expect(formatDateTime("2026-10-09T21:30:00Z", "Asia/Tashkent")).toBe("10.10.2026 02:30");
    expect(formatDateTime("2026-03-01T08:07:00Z", "UTC")).toBe("01.03.2026 08:07");
  });

  it("пустое значение — пустая строка", () => {
    expect(formatDateTime(null, "UTC")).toBe("");
  });
});
