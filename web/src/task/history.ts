// История задачи (§3.7) и значения полей по-русски: те же подписи — в окне конфликта версий.
import type { TaskHistoryView } from "../api/client";
import { priorityLabels, typeLabels, type Priority, type TaskType } from "../board/filters";
import { formatDuration } from "../lib/duration";
import { formatDate, formatDateTime } from "../lib/format";
import { statusLabel } from "../lib/status";

export const fieldLabels: Record<string, string> = {
  title: "Название",
  description: "Описание",
  status: "Статус",
  type: "Тип",
  priority: "Приоритет",
  assignee_id: "Исполнитель",
  sprint_id: "Спринт",
  due_date: "Срок",
  estimate_minutes: "Оценка",
  labels: "Метки",
  deleted_at: "Удаление",
};

/** Откуда брать имена людей и спринтов и в каком поясе показывать время. */
export interface ValueContext {
  userName: (id: string) => string | undefined;
  sprintName: (id: string) => string | undefined;
  timeZone?: string;
}

const EMPTY = "—";

/** Значение поля для показа; пустое — «—». */
export function formatFieldValue(field: string, v: unknown, ctx: ValueContext): string {
  if (v === null || v === undefined || v === "" || (Array.isArray(v) && v.length === 0)) return EMPTY;
  switch (field) {
    case "status":
      return statusLabel(String(v));
    case "type":
      return typeLabels[v as TaskType] ?? String(v);
    case "priority":
      return priorityLabels[v as Priority] ?? String(v);
    case "assignee_id":
      return ctx.userName(String(v)) ?? "пользователь не в проекте";
    case "sprint_id":
      return ctx.sprintName(String(v)) ?? "другой спринт";
    case "due_date":
      return formatDate(String(v), ctx.timeZone) || String(v);
    case "deleted_at":
      return formatDateTime(String(v), ctx.timeZone) || String(v);
    case "estimate_minutes":
      return typeof v === "number" ? formatDuration(v) : String(v);
    case "labels":
      return Array.isArray(v) ? v.join(", ") : String(v);
    case "description": {
      const s = String(v).trim().replace(/\s+/g, " ");
      return s.length > 200 ? `${s.slice(0, 200)}…` : s;
    }
    default:
      return typeof v === "string" ? v : JSON.stringify(v);
  }
}

export interface HistoryLine {
  id: string;
  who: string;
  when: string;
  text: string;
  note?: string;
  source?: string;
}

const sourceLabels: Record<string, string> = { telegram: "из Telegram", api: "через API" };

/** Запись истории одной строкой: «Статус: К выполнению → В работе». */
export function describeHistory(h: TaskHistoryView, ctx: ValueContext): HistoryLine {
  const field = h.field ?? "";
  let text: string;
  if (field === "deleted_at") {
    text = h.new_value == null ? "Задача восстановлена" : "Задача удалена";
  } else if (field === "status" && h.old_value == null) {
    text = `Задача создана: ${formatFieldValue(field, h.new_value, ctx)}`;
  } else {
    const label = fieldLabels[field] ?? field;
    text = `${label}: ${formatFieldValue(field, h.old_value, ctx)} → ${formatFieldValue(field, h.new_value, ctx)}`;
  }
  return {
    id: h.id ?? `${h.created_at}-${field}`,
    who: h.user_id ? (ctx.userName(h.user_id) ?? "Пользователь не в проекте") : "Система",
    when: formatDateTime(h.created_at, ctx.timeZone),
    text,
    note: h.note || undefined,
    source: h.source ? sourceLabels[h.source] : undefined,
  };
}
