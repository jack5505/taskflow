// Логика таймера (ТЗ §6.4), отделённая от разметки ради тестов.
import type { TaskTimerStart, TaskTimerStop, TaskTimerView } from "../api/client";
import { formatDuration } from "../lib/duration";

/**
 * Прошедшее время таймера в секундах на момент now. Сервер отдаёт elapsed_seconds на момент
 * ответа (fetchedAt), дальше досчитываем по своим часам: так расхождение часов браузера и
 * сервера не влияет на показ.
 */
export function elapsedSeconds(timer: TaskTimerView, fetchedAt: number, now: number): number {
  const base = timer.elapsed_seconds ?? 0;
  return Math.max(0, base + Math.floor(Math.max(0, now - fetchedAt) / 1000));
}

/**
 * Часы таймера «Ч:ММ:СС» — тикающему счётчику нужны секунды, поэтому не формат «2h 30m» из
 * §6.1; списанное время после остановки показывается уже в нём.
 */
export function formatClock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return `${h}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;
}

export interface TimerTask {
  status?: string;
  assignee_id?: string | null;
  deleted_at?: string | null;
}

/**
 * Кнопка «Старт» (§6.4): только исполнителю задачи в статусе «В работе», не в архивном проекте и
 * не на удалённой задаче. Проверяет сервер — здесь только скрываем кнопку.
 */
export function canStartTimer(t: TimerTask, meId: string | undefined, archived: boolean): boolean {
  return !!meId && !archived && !t.deleted_at && t.status === "in_progress" && t.assignee_id === meId;
}

/** Итог остановки: «TM-42: списано 47m» с пояснением, если ничего не записано или запись обрезана. */
export function stopMessage(r: TaskTimerStop): string {
  const key = r.task_key ? `${r.task_key}: ` : "";
  const minutes = r.minutes ?? 0;
  if (minutes === 0)
    return r.trimmed
      ? `${key}время не списано — дневной лимит 24h исчерпан`
      : `${key}меньше минуты, время не списано`;
  const spent = `${key}списано ${r.duration || formatDuration(minutes)}`;
  return r.trimmed ? `${spent} (обрезано по дневному лимиту 24h)` : spent;
}

/** Старт на другой задаче останавливает прежний таймер с записью времени — об этом сообщаем. */
export function startMessage(r: TaskTimerStart): string {
  return r.stopped ? `Прежний таймер остановлен. ${stopMessage(r.stopped)}` : "";
}

/** Ключ проекта из ключа задачи «TM-42» (§3.3). */
export function projectKeyOf(taskKey: string): string {
  return taskKey.replace(/-\d+$/, "");
}
