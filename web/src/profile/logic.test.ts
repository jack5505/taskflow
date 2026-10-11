import { describe, expect, it } from "vitest";
import {
  PERSONAL_EVENTS,
  changedNotifications,
  displayNameError,
  effectiveNotifications,
  formatCountdown,
  isKnownTimeZone,
  profileChanges,
  secondsLeft,
  telegramAccount,
} from "./logic";

describe("личные уведомления (§8.7)", () => {
  it("десять ключей API, по умолчанию все включены", () => {
    expect(PERSONAL_EVENTS.map((e) => e.key)).toEqual([
      "assigned",
      "rework",
      "accepted_cancelled",
      "comment",
      "review_requested",
      "due",
      "timer_auto_stopped",
      "worklog_trimmed",
      "task_deleted",
      "membership",
    ]);
    expect(Object.values(effectiveNotifications(undefined)).every(Boolean)).toBe(true);
    expect(Object.values(effectiveNotifications({}))).toHaveLength(10);
  });

  it("сохранённое значение важнее умолчания, чужие ключи отбрасываются", () => {
    const s = effectiveNotifications({ comment: false, unknown: false });
    expect(s.comment).toBe(false);
    expect(s.assigned).toBe(true);
    expect("unknown" in s).toBe(false);
  });

  it("в PATCH уходят только изменённые переключатели", () => {
    const before = effectiveNotifications({ due: false });
    expect(changedNotifications(before, { ...before })).toEqual({});
    expect(changedNotifications(before, { ...before, due: true, comment: false })).toEqual({
      due: true,
      comment: false,
    });
  });
});

describe("имя и часовой пояс (§3.1, §7.8)", () => {
  const me = { display_name: "Анна Смирнова", timezone: "Europe/Moscow" };

  it("без изменений — пустое тело", () => {
    expect(profileChanges(me, "Анна Смирнова", "Europe/Moscow")).toEqual({});
    expect(profileChanges(me, "  Анна Смирнова ", " Europe/Moscow ")).toEqual({});
  });

  it("только изменённые поля, с обрезкой пробелов", () => {
    expect(profileChanges(me, " Анна ", "Europe/Moscow")).toEqual({ display_name: "Анна" });
    expect(profileChanges(me, "Анна Смирнова", "Asia/Tashkent")).toEqual({ timezone: "Asia/Tashkent" });
  });

  it("имя от 1 до 80 символов после обрезки", () => {
    expect(displayNameError("   ")).not.toBe("");
    expect(displayNameError("А")).toBe("");
    expect(displayNameError("я".repeat(80))).toBe("");
    expect(displayNameError("я".repeat(81))).not.toBe("");
  });

  it("часовой пояс — только известный IANA", () => {
    expect(isKnownTimeZone("Europe/Moscow")).toBe(true);
    expect(isKnownTimeZone("UTC")).toBe(true);
    expect(isKnownTimeZone("Asia/Tashkent", ["Asia/Tashkent"])).toBe(true);
    expect(isKnownTimeZone("Moscow")).toBe(false);
    expect(isKnownTimeZone("")).toBe(false);
  });

  it("часовой пояс — в написании, которое примет сервер (Go LoadLocation)", () => {
    expect(isKnownTimeZone("europe/moscow")).toBe(false);
    expect(isKnownTimeZone("+03:00")).toBe(false);
    expect(isKnownTimeZone("-0500")).toBe(false);
  });
});

describe("Telegram (§8.1)", () => {
  it("привязан — @имя или id, не привязан — null", () => {
    expect(telegramAccount({ telegram_user_id: 42, telegram_username: "ivan" })).toBe("@ivan");
    expect(telegramAccount({ telegram_user_id: 42 })).toBe("id 42");
    expect(telegramAccount({ telegram_username: "ivan" })).toBeNull();
    expect(telegramAccount({})).toBeNull();
  });

  it("срок кода: остаток в секундах, истёкший — 0", () => {
    const now = Date.parse("2026-10-11T10:00:00Z");
    expect(secondsLeft("2026-10-11T10:10:00Z", now)).toBe(600);
    expect(secondsLeft("2026-10-11T10:00:00.400Z", now)).toBe(1);
    expect(secondsLeft("2026-10-11T09:59:00Z", now)).toBe(0);
    expect(secondsLeft("не дата", now)).toBe(0);
  });

  it("обратный отсчёт м:сс", () => {
    expect(formatCountdown(600)).toBe("10:00");
    expect(formatCountdown(65)).toBe("1:05");
    expect(formatCountdown(0)).toBe("0:00");
    expect(formatCountdown(-3)).toBe("0:00");
  });
});
