import { describe, expect, it } from "vitest";

import { parseInputDate, toInputDate } from "./date";

describe("parseInputDate", () => {
  it.each([
    ["05.03.2026", "2026-03-05"],
    ["5.3.2026", "2026-03-05"],
    [" 29.02.2028 ", "2028-02-29"],
    ["2026-03-05", "2026-03-05"],
    ["31.12.2026", "2026-12-31"],
  ])("%s → %s", (text, iso) => expect(parseInputDate(text)).toBe(iso));

  it("пустая строка — очистка", () => {
    expect(parseInputDate("")).toBe("");
    expect(parseInputDate("   ")).toBe("");
  });

  it.each(["05.03", "05.03.26", "31.02.2026", "29.02.2027", "00.01.2026", "01.13.2026", "1.1.1800", "05/03/2026", "завтра"])(
    "«%s» — не дата",
    (text) => expect(parseInputDate(text)).toBeNull(),
  );
});

describe("toInputDate", () => {
  it("YYYY-MM-DD → DD.MM.YYYY", () => expect(toInputDate("2026-03-05")).toBe("05.03.2026"));
  it("пусто и мусор — пусто", () => {
    expect(toInputDate("")).toBe("");
    expect(toInputDate(null)).toBe("");
    expect(toInputDate("2026-03-05T10:00:00Z")).toBe("");
  });
});
