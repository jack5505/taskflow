// Логика профиля (ТЗ §7.8): что отправить в PATCH /me, личные уведомления §8.7, код привязки
// Telegram §8.1. Проверки здесь — только подсказки до запроса, правила проверяет сервер.
import type { MeProfile, MeUpdate } from "../api/client";

/** Личные уведомления §8.7 — ключи notification_settings из API (api/internal/user/notifications.go). */
export const PERSONAL_EVENTS: { key: string; label: string }[] = [
  { key: "assigned", label: "Вас назначили исполнителем" },
  { key: "rework", label: "Вашу задачу вернули на доработку" },
  { key: "accepted_cancelled", label: "Вашу задачу приняли или отменили" },
  { key: "comment", label: "Новый комментарий к задаче, где вы исполнитель или автор" },
  { key: "review_requested", label: "Задача, где вы автор, отправлена на проверку" },
  { key: "due", label: "Срок задачи завтра и срок просрочен" },
  { key: "timer_auto_stopped", label: "Таймер остановлен автоматически после 12 часов" },
  { key: "worklog_trimmed", label: "Запись таймера обрезана по дневному лимиту 24 часа" },
  { key: "task_deleted", label: "Ваша задача удалена" },
  { key: "membership", label: "Добавление в проект, исключение из него, смена роли" },
];

/** Включённые уведомления: сохранённое значение, иначе умолчание §8.7 — все включены. */
export function effectiveNotifications(stored: Record<string, boolean> | undefined): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const e of PERSONAL_EVENTS) out[e.key] = stored?.[e.key] ?? true;
  return out;
}

/** Только изменённые переключатели — PATCH /me принимает частичный объект. */
export function changedNotifications(
  before: Record<string, boolean>,
  after: Record<string, boolean>,
): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const e of PERSONAL_EVENTS) if (before[e.key] !== after[e.key]) out[e.key] = after[e.key];
  return out;
}

/** Имя: 1–80 символов после обрезки пробелов (§3.1); пустая строка — ошибка. */
export function displayNameError(name: string): string {
  const n = [...name.trim()].length;
  return n >= 1 && n <= 80 ? "" : "Имя — от 1 до 80 символов";
}

/**
 * Пояс IANA для PATCH /me. Сервер проверяет его через Go time.LoadLocation — с учётом регистра и
 * без смещений вида «+03:00», а Intl мягче. Поэтому годится пояс из списка браузера или имя, которое
 * Intl знает и возвращает ровно в том же написании (так проходят ссылки вроде UTC, которых нет в списке).
 */
export function isKnownTimeZone(tz: string, zones: readonly string[] = []): boolean {
  const v = tz.trim();
  if (!v) return false;
  if (zones.includes(v)) return true;
  if (!/^[A-Za-z]/.test(v)) return false;
  try {
    return new Intl.DateTimeFormat("en-US", { timeZone: v }).resolvedOptions().timeZone === v;
  } catch {
    return false;
  }
}

/** Тело PATCH /me для имени и пояса: только изменённые поля; пустое — сохранять нечего. */
export function profileChanges(me: Pick<MeProfile, "display_name" | "timezone">, name: string, timezone: string): MeUpdate {
  const body: MeUpdate = {};
  if (name.trim() !== (me.display_name ?? "")) body.display_name = name.trim();
  if (timezone.trim() !== (me.timezone ?? "")) body.timezone = timezone.trim();
  return body;
}

/** Привязанный Telegram: «@имя», если имя известно, иначе id; null — не привязан. */
export function telegramAccount(me: Pick<MeProfile, "telegram_user_id" | "telegram_username">): string | null {
  if (!me.telegram_user_id) return null;
  return me.telegram_username ? `@${me.telegram_username}` : `id ${me.telegram_user_id}`;
}

/**
 * Код привязки Telegram (§8.1): 8 символов, действует 10 минут, ссылка https://t.me/<бот>?start=<код>.
 * Ожидаемый ответ POST /me/telegram/link-code — эндпоинт появится в issue #49.
 */
export interface TelegramLinkCode {
  code: string;
  expires_at: string;
  url: string;
}

/** Сколько секунд коду осталось жить; 0 — истёк (или срок не разобрать). */
export function secondsLeft(expiresAt: string, now: number): number {
  const t = Date.parse(expiresAt);
  if (Number.isNaN(t)) return 0;
  return Math.max(0, Math.ceil((t - now) / 1000));
}

/** Обратный отсчёт «м:сс», например 9:05. */
export function formatCountdown(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}
