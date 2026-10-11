import { describe, expect, it } from "vitest";
import { formatDuration, formatSpentEstimate } from "./duration";

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
