import { describe, expect, it } from "vitest";

import { AVATAR_COLORS, avatarColor } from "./avatar";

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
