// Мелкая логика доски, отделённая от разметки ради тестов.
import type { TaskListItem } from "../api/client";
import { BOARD_COLUMNS, type BoardStatus, type Status } from "./transitions";

/** Ключ кэша задач доски проекта; хвост — параметры запроса. */
export const boardQueryKey = (key: string) => ["board", key] as const;

/** Ключ мутаций перехода с доски: пока они идут, доску не перечитываем — ответ откатил бы карточку. */
export const moveKey = ["board-move"];

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

/**
 * Как задача выглядит сразу после перехода, до ответа сервера (§4): «отказаться» очищает
 * исполнителя, взятие свободной задачи назначает того, кто её взял.
 */
export function applyMove(t: TaskListItem, to: Status, meId: string): TaskListItem {
  const next: TaskListItem = { ...t, status: to };
  if (t.status === "in_progress" && to === "todo") next.assignee_id = undefined;
  if (t.status === "todo" && to === "in_progress" && !t.assignee_id) next.assignee_id = meId;
  return next;
}
