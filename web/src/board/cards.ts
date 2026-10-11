// Мелкая логика доски, отделённая от разметки ради тестов.
import type { TaskListItem } from "../api/client";
import { BOARD_COLUMNS, type BoardStatus } from "./transitions";

/** Ключ кэша задач доски проекта; хвост — параметры запроса. */
export const boardQueryKey = (key: string) => ["board", key] as const;

/** Инициалы для аватара: «Иван Петров» → «ИП». */
export function initials(name: string | undefined): string {
  return (name ?? "")
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => [...w][0]!.toUpperCase())
    .join("");
}

/** Задачи по колонкам в порядке ответа сервера; задачи вне колонок (отменённые) отбрасываются. */
export function groupByStatus(tasks: TaskListItem[]): Map<BoardStatus, TaskListItem[]> {
  const m = new Map<BoardStatus, TaskListItem[]>(BOARD_COLUMNS.map((s) => [s, []]));
  for (const t of tasks) m.get(t.status as BoardStatus)?.push(t);
  return m;
}
