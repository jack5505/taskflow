import { describe, expect, it } from "vitest";

import { AVATAR_COLORS, avatarColor, initials, memberColors } from "./avatar";

describe("avatarColor", () => {
  it("один id — всегда один цвет, в пределах палитры", () => {
    const id = "0b5c2a8e-3f41-4c2e-9d0a-1e2f3a4b5c6d";
    expect(avatarColor(id)).toBe(avatarColor(id));
    expect(avatarColor(id)).toBeGreaterThanOrEqual(1);
    expect(avatarColor(id)).toBeLessThanOrEqual(AVATAR_COLORS);
  });

  it("разные пользователи раскладываются по палитре, а не в один цвет", () => {
    const ids = Array.from({ length: 40 }, (_, i) => `user-${i}-${(i * 2654435761) >>> 0}`);
    expect(new Set(ids.map(avatarColor)).size).toBeGreaterThanOrEqual(6);
  });
});

describe("memberColors", () => {
  it("у участников проекта — разные цвета, пока хватает палитры; порядок списка не важен", () => {
    const ids = ["c", "a", "b", undefined, "a"];
    const m = memberColors(ids);
    expect([...m.entries()]).toEqual([
      ["a", 1],
      ["b", 2],
      ["c", 3],
    ]);
    expect(memberColors(["b", "c", "a"])).toEqual(m);
    const many = memberColors(Array.from({ length: AVATAR_COLORS }, (_, i) => `u${i}`));
    expect(new Set(many.values()).size).toBe(AVATAR_COLORS);
  });
});

describe("initials", () => {
  it.each([
    ["Иван Петров", "ИП"],
    ["  анна  ", "А"],
    ["Мария Анна Сидорова", "МА"],
    ["", ""],
    [undefined, ""],
  ])("%s → %s", (name, want) => expect(initials(name)).toBe(want));
});
