// Создание задачи (ТЗ §7.6): проверка формы, права на поля, горячая клавиша C.
import type { QueryKey } from "@tanstack/react-query";
import type { MeProfile, ProjectListItem, SprintView, TaskCreateRequest } from "../api/client";
import type { FieldError } from "../api/errors";
import { parseDuration } from "../lib/duration";
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
  if (f.dueDate) body.due_date = f.dueDate;
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
  const p = /^\/projects\/([^/]+)/.exec(pathname);
  if (p) return decodeURIComponent(p[1]);
  const t = /^\/tasks\/(.+)-\d+\/?$/.exec(pathname);
  return t ? decodeURIComponent(t[1]) : null;
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
 * Клавиша C без модификаторов (Ctrl+C — копирование). По code KeyC: в русской раскладке та же
 * клавиша даёт «с», и пользователю не нужно переключать раскладку.
 */
export function isCreateHotkey(e: HotkeyEvent): boolean {
  if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || e.repeat || e.isComposing || e.defaultPrevented) {
    return false;
  }
  return e.code === "KeyC" || e.key === "c" || e.key === "C" || e.key === "с" || e.key === "С";
}

/** Фокус в поле ввода: там C — это буква, а не команда. */
export function isTypingTarget(t: EventTarget | null): boolean {
  if (!t || typeof (t as Element).tagName !== "string") return false;
  const el = t as HTMLElement;
  if (el.isContentEditable) return true;
  const tag = el.tagName.toLowerCase();
  return tag === "input" || tag === "textarea" || tag === "select";
}

/** Открыт модальный диалог (переход статуса, конфликт версий, само создание). */
export function hasOpenDialog(doc: Pick<Document, "querySelector">): boolean {
  return doc.querySelector('[aria-modal="true"], dialog[open]') !== null;
}

/**
 * Какие запросы устарели после создания задачи в проекте: всё, что ключено этим проектом
 * (доска ["board", key, …], бэклог, спринты с числом задач), и сводки главной.
 */
export function affectedByCreate(queryKey: QueryKey, projectKey: string): boolean {
  if (queryKey[0] === projectsQueryKey[0] || queryKey[0] === myTasksQueryKey[0]) return true;
  return queryKey[1] === projectKey;
}

export const sprintsQueryKey = (key: string) => ["project-sprints", key] as const;
