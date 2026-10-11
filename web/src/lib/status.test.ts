import { describe, expect, it } from "vitest";
import { statusLabel } from "./status";

describe("statusLabel", () => {
  it.each([
    ["todo", "К выполнению"],
    ["in_progress", "В работе"],
    ["review", "На проверке"],
    ["done", "Готово"],
    ["cancelled", "Отменена"],
  ])("%s → %s", (s, want) => {
    expect(statusLabel(s)).toBe(want);
  });

  it("неизвестный и пустой", () => {
    expect(statusLabel("weird")).toBe("weird");
    expect(statusLabel(undefined)).toBe("");
  });
});
