// Логика страницы «Бэклог и спринты» (ТЗ §7.4, §5), отделённая от разметки ради тестов.
import type { SprintView, TaskListItem } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { boardQueryKey } from "../board/cards";

/** Ключ кэша спринтов проекта. */
export const sprintsQueryKey = (key: string) => ["sprints", key] as const;

/**
 * Ключ кэша задач бэклога — под ключом доски: карточка задачи и доска уже помечают его
 * устаревшим после своих изменений, бэклог перечитается сам.
 */
export const backlogQueryKey = (key: string) => [...boardQueryKey(key), "backlog"] as const;

/** Снимок страницы: спринты проекта и задачи незакрытых спринтов и бэклога. */
export interface BacklogSnapshot {
  sprints: SprintView[];
  tasks: TaskListItem[];
}

/** Секция бэклога: id спринта или BACKLOG. */
export const BACKLOG = "backlog";

export interface Section {
  id: string;
  /** Нет у бэклога. */
  sprint?: SprintView;
  tasks: TaskListItem[];
}

export const sectionOf = (t: TaskListItem): string => t.sprint_id || BACKLOG;

/** Незакрытые спринты (активный и запланированные) — секции страницы; закрытые — в архиве. */
export function openSprints(sprints: SprintView[]): SprintView[] {
  return sprints.filter((s) => s.state === "active" || s.state === "planned");
}

/**
 * Секции сверху вниз (§7.4): активный спринт, запланированные в порядке сервера, бэклог. Задачи —
 * в порядке ответа сервера (ручной порядок секции); задачи чужих спринтов отбрасываются.
 */
export function buildSections(sprints: SprintView[], tasks: TaskListItem[]): Section[] {
  const open = openSprints(sprints);
  const ordered = [...open.filter((s) => s.state === "active"), ...open.filter((s) => s.state === "planned")];
  const sections: Section[] = [...ordered.map((s) => ({ id: s.id!, sprint: s, tasks: [] })), { id: BACKLOG, tasks: [] }];
  const byId = new Map(sections.map((s) => [s.id, s]));
  for (const t of tasks) byId.get(sectionOf(t))?.tasks.push(t);
  return sections;
}

/** Итог секции: число задач и сумма оценок в минутах (без оценки — 0). */
export function totals(tasks: TaskListItem[]): { count: number; estimate: number; withoutEstimate: number } {
  let estimate = 0;
  let withoutEstimate = 0;
  for (const t of tasks) {
    if (t.estimate_minutes == null) withoutEstimate++;
    else estimate += t.estimate_minutes;
  }
  return { count: tasks.length, estimate, withoutEstimate };
}

/** Куда бросили задачу: на строку другой задачи или на секцию (тогда — в её конец). */
export type DropOver = { kind: "row"; taskId: string } | { kind: "section"; sectionId: string };

export interface MoveTarget {
  /** Секция назначения. */
  sectionId: string;
  /** after_task_id для POST /tasks/{key}/move: null — в начало секции. */
  afterTaskId: string | null;
  /** Смена секции — только менеджер (§2). */
  crossSection: boolean;
}

/**
 * Место задачи после перетаскивания. На строку: вниз по своей секции — встаёт после неё, иначе —
 * перед ней; на секцию — в конец. null — задача остаётся на своём месте.
 */
export function dropTarget(sections: Section[], taskId: string, over: DropOver): MoveTarget | null {
  const from = sections.find((s) => s.tasks.some((t) => t.id === taskId));
  const to =
    over.kind === "row"
      ? sections.find((s) => s.tasks.some((t) => t.id === over.taskId))
      : sections.find((s) => s.id === over.sectionId);
  if (!from || !to || (over.kind === "row" && over.taskId === taskId)) return null;
  const rest = to.tasks.filter((t) => t.id !== taskId);
  let after: string | null;
  if (over.kind === "section") {
    after = rest.at(-1)?.id ?? null;
  } else {
    const fromIdx = from.tasks.findIndex((t) => t.id === taskId);
    const overIdx = to.tasks.findIndex((t) => t.id === over.taskId);
    const restIdx = rest.findIndex((t) => t.id === over.taskId);
    after = from === to && fromIdx < overIdx ? over.taskId : (rest[restIdx - 1]?.id ?? null);
  }
  if (from === to) {
    const prev = from.tasks[from.tasks.findIndex((t) => t.id === taskId) - 1]?.id ?? null;
    if (prev === after) return null;
  }
  return { sectionId: to.id, afterTaskId: after, crossSection: from !== to };
}

/** Где задача стоит сейчас — в виде места для applyMove (откат неудачного переноса). */
export function currentPlace(sections: Section[], taskId: string): MoveTarget | null {
  for (const s of sections) {
    const i = s.tasks.findIndex((t) => t.id === taskId);
    if (i >= 0) return { sectionId: s.id, afterTaskId: s.tasks[i - 1]?.id ?? null, crossSection: false };
  }
  return null;
}

/**
 * Как список выглядит сразу после переноса, до ответа сервера: задача встаёт после afterTaskId
 * или в начало секции. Порядок остальных задач не меняется.
 */
export function applyMove(tasks: TaskListItem[], taskId: string, target: MoveTarget): TaskListItem[] {
  const task = tasks.find((t) => t.id === taskId);
  if (!task) return tasks;
  const moved: TaskListItem = { ...task, sprint_id: target.sectionId === BACKLOG ? undefined : target.sectionId };
  const rest = tasks.filter((t) => t.id !== taskId);
  // Сразу после afterTaskId или перед первой задачей секции; пустая секция — в конец списка.
  const anchor = target.afterTaskId
    ? rest.findIndex((t) => t.id === target.afterTaskId)
    : rest.findIndex((t) => sectionOf(t) === target.sectionId);
  const idx = anchor < 0 ? rest.length : target.afterTaskId ? anchor + 1 : anchor;
  return [...rest.slice(0, idx), moved, ...rest.slice(idx)];
}

/** Сводка перед завершением (§5.4): готовые, незакрытые (уедут), отменённые (останутся). */
export function closeSummary(tasks: TaskListItem[]): { done: number; open: number; cancelled: number } {
  const s = { done: 0, open: 0, cancelled: 0 };
  for (const t of tasks) {
    if (t.status === "done") s.done++;
    else if (t.status === "cancelled") s.cancelled++;
    else s.open++;
  }
  return s;
}

/**
 * Сводка закрытого спринта по задачам, что в нём остались (§5.4: готовые и отменённые; незакрытые
 * перенесены при завершении): готово, отменено, оценка готовых, списано по всем.
 */
export function archiveSummary(tasks: TaskListItem[]): { done: number; cancelled: number; doneEstimate: number; spent: number } {
  const s = { done: 0, cancelled: 0, doneEstimate: 0, spent: 0 };
  for (const t of tasks) {
    if (t.status === "done") {
      s.done++;
      s.doneEstimate += t.estimate_minutes ?? 0;
    } else if (t.status === "cancelled") s.cancelled++;
    s.spent += t.spent_minutes ?? 0;
  }
  return s;
}

/** Наибольшая длительность спринта в днях (§5.2). */
export const MAX_SPRINT_DAYS = 42;

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/** Дни между датами ГГГГ-ММ-ДД; не дата — NaN. */
export function daysBetween(start: string, end: string): number {
  if (!DATE_RE.test(start) || !DATE_RE.test(end)) return NaN;
  return Math.round((Date.parse(`${end}T00:00:00Z`) - Date.parse(`${start}T00:00:00Z`)) / 86_400_000);
}

/** Даты спринта (§5.2). required — для запуска обе обязательны. "" — даты годятся. */
export function sprintDatesError(start: string, end: string, required: boolean): string {
  if (!start || !end) return required ? "Для запуска нужны даты начала и окончания" : "";
  const days = daysBetween(start, end);
  if (!(days >= 1)) return "Окончание — позже начала";
  if (days > MAX_SPRINT_DAYS) return `Спринт не длиннее ${MAX_SPRINT_DAYS} дней`;
  return "";
}

/** Дата ГГГГ-ММ-ДД через n дней (в UTC, без времени). */
export function addDays(date: string, n: number): string {
  const d = new Date(`${date}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

/** Сегодня (или дата момента at) ГГГГ-ММ-ДД в поясе пользователя (без пояса — браузера). */
export function today(timeZone?: string, at: Date = new Date()): string {
  const opts = { year: "numeric", month: "2-digit", day: "2-digit" } as const;
  let f: Intl.DateTimeFormat;
  try {
    f = new Intl.DateTimeFormat("en-CA", { ...opts, timeZone });
  } catch {
    f = new Intl.DateTimeFormat("en-CA", opts);
  }
  return f.format(at);
}

/** PATCH перед запуском: только изменённые в окне цель и даты; ничего не изменилось — null. */
export function startPatch(
  sprint: SprintView,
  f: { goal: string; start: string; end: string },
): { goal?: string | null; start_date?: string; end_date?: string } | null {
  const patch: { goal?: string | null; start_date?: string; end_date?: string } = {};
  const goal = f.goal.trim();
  if (goal !== (sprint.goal ?? "").trim()) patch.goal = goal || null;
  if (f.start !== (sprint.start_date ?? "")) patch.start_date = f.start;
  if (f.end !== (sprint.end_date ?? "")) patch.end_date = f.end;
  return Object.keys(patch).length ? patch : null;
}

const fieldLabels: Record<string, string> = {
  name: "Название",
  goal: "Цель",
  start_date: "Начало",
  end_date: "Окончание",
  move_open_to: "Куда перенести",
};

/**
 * Текст ошибки спринта для окна: сообщение сервера (§9.3), а при VALIDATION_FAILED — ещё и
 * причины по полям, иначе пользователь не узнает, что именно не так.
 */
export function sprintErrorText(e: unknown): string {
  const fields = fieldErrors(parseApiError(e));
  const base = errorText(e);
  if (!fields.length) return base;
  return `${base}: ${fields.map((f) => `${fieldLabels[f.field] ?? f.field} — ${f.reason}`).join("; ")}`;
}
