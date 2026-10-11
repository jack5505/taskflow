import { describe, expect, it } from "vitest";
import { newPasswordError } from "./password";

describe("правила нового пароля (§7.1)", () => {
  it("не короче 10 символов — в символах, а не в единицах UTF-16", () => {
    expect(newPasswordError("short")).toBe("Пароль должен быть не короче 10 символов");
    expect(newPasswordError("😀".repeat(9))).not.toBe(""); // 9 символов, 18 единиц UTF-16
    expect(newPasswordError("пароль1234")).toBe("");
  });

  it("не длиннее 72 байт", () => {
    expect(newPasswordError("пароль".repeat(6))).toBe(""); // 72 байта
    expect(newPasswordError("пароль".repeat(7))).toMatch(/72 байт/); // 84 байта
  });
});
