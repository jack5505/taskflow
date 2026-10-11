// Записи времени в карточке задачи (ТЗ §6.1, §6.3, §6.5). Права только скрывают кнопки — проверяет сервер.
import type { TaskTask, TaskWorklogView } from "../api/client";
import { parseApiError, errorText, fieldErrors } from "../api/errors";
import type { Actor } from "../board/transitions";
import { addDays, today } from "../backlog/logic";
import { formatDuration, parseDuration } from "../lib/duration";
import { formatDate } from "../lib/format";

/** Запись не больше суток (§3.5). */
export const MAX_WORKLOG_MINUTES = 24 * 60;
/** Комментарий записи — до 500 символов (§3.5). */
export const MAX_WORKLOG_COMMENT = 500;
/** Автор правит свою запись 7 календарных дней после work_date (§6.5). */
export const EDIT_WINDOW_DAYS = 7;

export const sourceLabels: Record<NonNullable<TaskWorklogView["source"]>, string> = {
  manual: "вручную",
  timer: "таймер",
  telegram: "из Telegram",
};

/** Календарная дата ГГГГ-ММ-ДД момента времени в поясе пользователя (без пояса — браузера). */
export function dayOf(value: string | undefined, timeZone?: string): string {
  if (!value) return "";
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? "" : today(timeZone, d);
}

/**
 * «Списать время» (§2, §6.3): исполнитель задачи или менеджер. readOnly — проект в архиве,
 * задача удалена или её спринт завершён (§6.5). На отменённую задачу время не списывается.
 */
export function canAddWorklog(t: TaskTask, actor: Actor | null, readOnly: boolean): boolean {
  if (!actor || readOnly || t.status === "cancelled") return false;
  return actor.manager || (!!t.assignee_id && t.assignee_id === actor.userId);
}

export interface WorklogPerms {
  edit: boolean;
  remove: boolean;
}

/**
 * Правка и удаление записи (§6.5): менеджер — любую без срока, автор — свою в течение 7 дней
 * после work_date. today — сегодня в поясе пользователя. Отменённую задачу можно только удалять.
 */
export function worklogPerms(
  w: TaskWorklogView,
  actor: Actor | null,
  opts: { readOnly: boolean; cancelled: boolean; today: string },
): WorklogPerms {
  if (!actor || opts.readOnly) return { edit: false, remove: false };
  const own = !!w.user_id && w.user_id === actor.userId;
  const ok = actor.manager || (own && !!w.work_date && opts.today <= addDays(w.work_date, EDIT_WINDOW_DAYS));
  return { edit: ok && !opts.cancelled, remove: ok };
}

export interface WorklogDraft {
  duration: string;
  workDate: string;
  comment: string;
}

/** Ошибки полей формы до отправки; пустой объект — можно отправлять. */
export type DraftErrors = Partial<Record<keyof WorklogDraft, string>>;

/**
 * Проверка формы (§3.5, §6.1, §6.3): длительность обязательна, от 1m до 24h; дата не позже
 * сегодня и не раньше minDate (дата создания задачи; для автора при правке — ещё и 7 дней назад).
 */
export function validateDraft(d: WorklogDraft, range: { today: string; minDate?: string; keepDate?: string }): DraftErrors {
  const e: DraftErrors = {};
  const m = d.duration.trim() ? parseDuration(d.duration) : null;
  if (!d.duration.trim()) e.duration = "Укажите длительность";
  else if (m === null) e.duration = "Формат: 2h 30m, 45m, 3h, 1d (1d = 8h)";
  else if (m < 1 || m > MAX_WORKLOG_MINUTES) e.duration = "От 1m до 24h";
  // Дату записи, которую не меняют, сервер не проверяет: её «сегодня» — по поясу автора записи.
  const kept = !!range.keepDate && d.workDate === range.keepDate;
  if (!d.workDate) e.workDate = "Укажите дату";
  else if (kept) {
    // не проверяем
  } else if (d.workDate > range.today) e.workDate = "Не позже сегодняшней даты";
  else if (range.minDate && d.workDate < range.minDate) e.workDate = `Не раньше ${formatDate(range.minDate)}`;
  if ([...d.comment.trim()].length > MAX_WORKLOG_COMMENT) e.comment = "Не длиннее 500 символов";
  return e;
}

/**
 * Текст ошибки сервера для формы. DAILY_LIMIT_EXCEEDED — со свободным остатком дня; ошибка
 * поля (VALIDATION_FAILED) — с причиной, а не общее «Некорректные данные».
 */
export function worklogErrorText(e: unknown): string {
  const err = parseApiError(e);
  if (!err) return errorText(e);
  if (err.code === "DAILY_LIMIT_EXCEEDED") {
    const used = minutesOf(err.details.used_minutes);
    const left = minutesOf(err.details.available_minutes);
    const parts = ["За один день можно списать не больше 24 часов"];
    if (used !== null) parts.push(`уже списано ${formatDuration(used)}`);
    if (left !== null) parts.push(`доступно ${formatDuration(left)}`);
    return parts.join(", ");
  }
  const field = fieldErrors(err)[0];
  if (err.code === "VALIDATION_FAILED" && field) {
    const names: Record<string, string> = { work_date: "Дата", comment: "Комментарий", duration: "Длительность" };
    return `${names[field.field] ?? field.field}: ${field.reason}`;
  }
  return errorText(e);
}

function minutesOf(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}
