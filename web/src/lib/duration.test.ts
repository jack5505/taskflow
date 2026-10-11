import { describe, expect, it } from "vitest";
import { formatDuration, formatSpentEstimate, parseDuration } from "./duration";

describe("formatDuration", () => {
  it.each([
    [0, "0m"],
    [1, "1m"],
    [45, "45m"],
    [60, "1h"],
    [150, "2h 30m"],
    [180, "3h"],
    [480, "8h"], // 1d при вводе, показываем часами
    [601, "10h 1m"],
    [60000, "1000h"],
  ])("%i мин → %s", (min, want) => {
    expect(formatDuration(min)).toBe(want);
  });

  it("пусто и некорректное — пустая строка", () => {
    expect(formatDuration(null)).toBe("");
    expect(formatDuration(undefined)).toBe("");
    expect(formatDuration(-5)).toBe("");
    expect(formatDuration(Number.NaN)).toBe("");
  });
});

describe("formatSpentEstimate", () => {
  it("списано и оценка", () => {
    expect(formatSpentEstimate(150, 240)).toBe("2h 30m / 4h");
  });
  it("без оценки — прочерк, без списаний — 0m", () => {
    expect(formatSpentEstimate(0, undefined)).toBe("0m / —");
    expect(formatSpentEstimate(undefined, 30)).toBe("0m / 30m");
  });
});

describe("parseDuration", () => {
  it.each([
    ["2h 30m", 150],
    ["2h30m", 150],
    ["45m", 45],
    ["3h", 180],
    ["1d", 480],
    ["1d 2h 5m", 605],
    ["  1D  ", 480],
  ])("%s → %i", (s, want) => {
    expect(parseDuration(s)).toBe(want);
  });
  it.each(["", "  ", "30m 2h", "2", "1.5h", "2x", "h", "-1h", "2h 3h"])("%s — не формат", (s) => {
    expect(parseDuration(s)).toBeNull();
  });
});
