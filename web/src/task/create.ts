// Создание задачи (ТЗ §7.6): проверка формы, права на поля, горячая клавиша C.
import type { QueryKey } from "@tanstack/react-query";
import type { MeProfile, ProjectListItem, SprintView, TaskCreateRequest } from "../api/client";
import type { FieldError } from "../api/errors";
import { INVALID_DATE, INVALID_DATE_TEXT } from "../lib/date";
import { parseDuration } from "../lib/duration";
import { membersQueryKey, projectQueryKey } from "../projects/hooks";
import { myTasksQueryKey, projectsQueryKey } from "../projects/logic";
import { parseLabels } from "./patch";

/** Значения формы как в полях ввода: строки, разбор — в validateCreate. */
export interface CreateForm {
  projectKey: string;
  title: string;
  type: NonNullable<TaskCreateRequest["type"]>;
  priority: NonNullable<TaskCreateRequest["priority"]>;
  description: string;
  assigneeId: string;
  sprintId: string;
  /** Оценка в формате 2h 30m (§6.1). */
  estimate: string;
  /** yyyy-mm-dd из <input type="date">. */
  dueDate: string;
  /** Через запятую. */
  labels: string;
}

/** Пустая форма; проект предвыбран текущий (§7.6). Тип и приоритет — умолчания §3.3. */
export function emptyForm(projectKey: string): CreateForm {
  return {
    projectKey,
    title: "",
    type: "task",
    priority: "medium",
    description: "",
    assigneeId: "",
    sprintId: "",
    estimate: "",
    dueDate: "",
    labels: "",
  };
}

/** Поле формы, к которому относится ошибка; general — ошибка без поля. */
export type FormField =
  | "project"
  | "title"
  | "type"
  | "priority"
  | "description"
  | "assignee"
  | "sprint"
  | "estimate"
  | "dueDate"
  | "labels"
  | "general";

export type FormErrors = Partial<Record<FormField, string>>;

/**
 * Проверить форму и собрать тело POST /projects/{key}/tasks. Обязательны только проект и
 * название (§7.6). Исполнитель и спринт уходят, только если поля видны (canManage): иначе
 * сервер ответил бы 403. Пустые необязательные поля не отправляются — сервер ставит умолчания.
 */
export function validateCreate(
  f: CreateForm,
  canManage: boolean,
): { body: TaskCreateRequest; errors: FormErrors } {
  const errors: FormErrors = {};
  if (!f.projectKey) errors.project = "Выберите проект";

  const title = f.title.trim();
  const n = [...title].length;
  if (n < 1 || n > 200) errors.title = "Название — от 1 до 200 символов";

  if ([...f.description].length > 20000) errors.description = "Описание — до 20 000 символов";

  let estimate: number | undefined;
  if (f.estimate.trim()) {
    const m = parseDuration(f.estimate);
    if (m === null) errors.estimate = "Формат: 2h 30m, 45m, 3h, 1d";
    else if (m < 1 || m > 60000) errors.estimate = "От 1m до 1000h";
    else estimate = m;
  }

  const labels = parseLabels(f.labels);
  if (labels.length > 10) errors.labels = "Не больше 10 меток";
  else if (labels.some((l) => [...l].length > 30)) errors.labels = "Метка — до 30 символов";

  const body: TaskCreateRequest = { title, type: f.type, priority: f.priority };
  if (f.description.trim()) body.description = f.description;
  if (estimate !== undefined) body.estimate_minutes = estimate;
  if (f.dueDate === INVALID_DATE) errors.dueDate = INVALID_DATE_TEXT;
  else if (f.dueDate) body.due_date = f.dueDate;
  if (labels.length) body.labels = labels;
  if (canManage && f.assigneeId) body.assignee_id = f.assigneeId;
  if (canManage && f.sprintId) body.sprint_id = f.sprintId;
  return { body, errors };
}

// Имена полей API (§9.3 details.fields) → поля формы.
const apiFields: Record<string, FormField> = {
  title: "title",
  type: "type",
  priority: "priority",
  description: "description",
  assignee_id: "assignee",
  sprint_id: "sprint",
  estimate_minutes: "estimate",
  due_date: "dueDate",
  labels: "labels",
};

/**
 * Ошибки валидации сервера — к полям формы. Путь вида labels[3] относится к полю labels.
 * Ошибка по полю, которого в форме не видно, уходит в общую — иначе пользователь её не увидит.
 */
export function apiFieldErrors(fields: FieldError[], canManage: boolean): FormErrors {
  const out: FormErrors = {};
  for (const fe of fields) {
    let f: FormField = apiFields[fe.field.replace(/[[.].*$/, "")] ?? "general";
    if (!canManage && (f === "assignee" || f === "sprint")) f = "general";
    const text = f === "general" ? `${fe.field}: ${fe.reason}` : fe.reason;
    out[f] = out[f] ? `${out[f]}; ${text}` : text;
  }
  return out;
}

/** Проекты, где можно создать задачу: не архивные и не удалённые (§3.2), по ключу. */
export function creatableProjects(list: ProjectListItem[] | undefined): ProjectListItem[] {
  return (list ?? [])
    .filter((p) => p.key && !p.is_archived && !p.deleted_at)
    .sort((a, b) => (a.key ?? "").localeCompare(b.key ?? ""));
}

/** Исполнителя и спринт задаёт менеджер проекта или администратор системы (§2, §7.6). */
export function canManageProject(me: MeProfile | undefined, p: ProjectListItem | undefined): boolean {
  if (!me || !p) return false;
  return me.is_system_admin === true || p.my_role === "manager";
}

/** В задачу можно поставить запланированный или активный спринт: завершённый сервер отклонит. */
export function openSprints(list: SprintView[] | undefined): SprintView[] {
  return (list ?? []).filter((s) => s.id && s.state !== "closed");
}

/**
 * Ключ проекта текущей страницы: /projects/TM, /projects/TM/backlog, /tasks/TM-42.
 * null — страница не проектная, клавиша C там не работает (§7.6).
 */
export function projectKeyFromPath(pathname: string): string | null {
  const m = /^\/projects\/([^/]+)/.exec(pathname) ?? /^\/tasks\/(.+)-\d+\/?$/.exec(pathname);
  if (!m) return null;
  try {
    return decodeURIComponent(m[1]);
  } catch {
    return null; // битая %-последовательность в набранном вручную адресе — не роняем шапку
  }
}

/** Что нужно от события клавиатуры, чтобы решить про горячую клавишу. */
export interface HotkeyEvent {
  key: string;
  code: string;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
  repeat: boolean;
  isComposing?: boolean;
  defaultPrevented: boolean;
}

/**
 * Клавиша C без модификаторов (Ctrl+C — копирование). По символу, а не по code: в русской
 * раскладке та же клавиша даёт «с» — тоже подходит; в Dvorak C — там, где на ней написано.
 */
export function isCreateHotkey(e: HotkeyEvent): boolean {
  if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || e.repeat || e.isComposing || e.defaultPrevented) {
    return false;
  }
  return ["c", "C", "с", "С"].includes(e.key);
}

/** Фокус в поле ввода: там C — это буква, а не команда. */
export function isTypingTarget(t: EventTarget | null): boolean {
  if (!t || typeof (t as Element).tagName !== "string") return false;
  const el = t as HTMLElement;
  if (el.isContentEditable) return true;
  const tag = el.tagName.toLowerCase();
  return tag === "input" || tag === "textarea" || tag === "select";
}

/**
 * Открыт модальный диалог (переход статуса, конфликт версий, само создание) или с клавиатуры
 * тащат карточку доски (dnd-kit ставит взятой карточке aria-pressed) — клавиша C не срабатывает.
 */
export function hasOpenDialog(doc: Pick<Document, "querySelector">): boolean {
  return doc.querySelector('[aria-modal="true"], dialog[open], [aria-roledescription][aria-pressed="true"]') !== null;
}

// Запросы проекта, которые новая задача не меняет: карточка проекта и участники.
const unaffected = new Set<unknown>([projectQueryKey("")[0], membersQueryKey("")[0]]);

/**
 * Как обновить запрос после создания задачи в проекте. "refetch" — списки задач проекта
 * (доска ["board", key, …], бэклог, спринты с числом задач): перечитываем, если на экране.
 * "stale" — счётчики главной и «Мои задачи»: только помечаем, перезапросит главная (как на доске).
 */
export function affectedByCreate(queryKey: QueryKey, projectKey: string): "refetch" | "stale" | null {
  if (queryKey[0] === projectsQueryKey[0] || queryKey[0] === myTasksQueryKey[0]) return "stale";
  return queryKey[1] === projectKey && !unaffected.has(queryKey[0]) ? "refetch" : null;
}

export const sprintsQueryKey = (key: string) => ["project-sprints", key] as const;
