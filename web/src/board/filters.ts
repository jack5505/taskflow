// Фильтры доски (ТЗ §7.3) живут в адресе страницы: перезагрузка их не теряет.
// ?assignee=<uuid>&assignee=none&mine=1&type=bug&priority=high&label=api&q=вход

import { BOARD_COLUMNS } from "./transitions";

export const TASK_TYPES = ["task", "bug", "feature"] as const;
export type TaskType = (typeof TASK_TYPES)[number];
export const PRIORITIES = ["low", "medium", "high", "critical"] as const;
export type Priority = (typeof PRIORITIES)[number];

export const typeLabels: Record<TaskType, string> = { task: "Задача", bug: "Ошибка", feature: "Новая функция" };
export const priorityLabels: Record<Priority, string> = {
  low: "Низкий",
  medium: "Средний",
  high: "Высокий",
  critical: "Критический",
};

/** Значение «Без исполнителя» в адресе. */
export const NO_ASSIGNEE = "none";

export interface BoardFilter {
  /** id исполнителей и/или NO_ASSIGNEE. */
  assignees: string[];
  /** «Только мои» — заменяет выбор исполнителей. */
  mine: boolean;
  types: TaskType[];
  priorities: Priority[];
  label: string;
  q: string;
}

export const emptyFilter: BoardFilter = { assignees: [], mine: false, types: [], priorities: [], label: "", q: "" };

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function pick<T extends string>(values: string[], allowed: readonly T[]): T[] {
  return allowed.filter((v) => values.includes(v));
}

/** Фильтр из адреса. Мусор (неизвестный тип, не-UUID) отбрасываем — иначе сервер ответит 400. */
export function filterFromParams(params: URLSearchParams): BoardFilter {
  const assignees = [...new Set(params.getAll("assignee"))].filter((v) => v === NO_ASSIGNEE || UUID_RE.test(v));
  return {
    assignees,
    mine: params.get("mine") === "1",
    types: pick(params.getAll("type"), TASK_TYPES),
    priorities: pick(params.getAll("priority"), PRIORITIES),
    label: params.get("label") ?? "",
    q: params.get("q") ?? "",
  };
}

/** Адрес для фильтра; пустые поля в адрес не попадают. */
export function paramsFromFilter(f: BoardFilter): URLSearchParams {
  const p = new URLSearchParams();
  for (const a of f.assignees) p.append("assignee", a);
  if (f.mine) p.set("mine", "1");
  for (const t of f.types) p.append("type", t);
  for (const pr of f.priorities) p.append("priority", pr);
  if (f.label) p.set("label", f.label);
  if (f.q) p.set("q", f.q);
  return p;
}

export function isFilterEmpty(f: BoardFilter): boolean {
  return paramsFromFilter(f).size === 0;
}

/** Переключить значение в списке (множественный выбор). */
export function toggle<T>(list: T[], value: T): T[] {
  return list.includes(value) ? list.filter((v) => v !== value) : [...list, value];
}

export interface TasksQuery {
  status: string[];
  sprint_id: string[];
  assignee_id?: string[];
  type?: string[];
  priority?: string[];
  label?: string;
  q?: string;
  limit: number;
}

/** Страница списка задач — максимум, который отдаёт сервер. */
export const PAGE_LIMIT = 200;

/**
 * Параметры GET /projects/{key}/tasks для доски: задачи активного спринта в статусах колонок
 * (отменённые не показываются), «Без исполнителя» — null, «Только мои» — id пользователя.
 */
export function tasksQuery(f: BoardFilter, sprintId: string, meId: string | undefined): TasksQuery {
  const q: TasksQuery = { status: [...BOARD_COLUMNS], sprint_id: [sprintId], limit: PAGE_LIMIT };
  const assignees = f.mine ? (meId ? [meId] : []) : f.assignees.map((a) => (a === NO_ASSIGNEE ? "null" : a));
  if (assignees.length) q.assignee_id = assignees;
  if (f.types.length) q.type = f.types;
  if (f.priorities.length) q.priority = f.priorities;
  if (f.label.trim()) q.label = f.label.trim();
  if (f.q.trim()) q.q = f.q.trim();
  return q;
}
