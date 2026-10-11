// Сохранение карточки задачи: PATCH только изменённых полей с If-Match текущей версии и
// разбор 409 VERSION_CONFLICT (§3.3). Чужие правки не перезаписываются молча: пользователь
// видит, что поменял другой, и решает, повторить ли свои изменения поверх актуальной версии.
import type { TaskTask, TaskUpdateRequest } from "../api/client";
import { parseApiError } from "../api/errors";

/** Поля, которые карточка меняет через PATCH. */
export const EDITABLE_FIELDS = [
  "title",
  "description",
  "type",
  "priority",
  "assignee_id",
  "due_date",
  "estimate_minutes",
  "labels",
] as const;
export type EditableField = (typeof EDITABLE_FIELDS)[number];
export type Edits = Partial<Pick<TaskTask, EditableField>>;

/** Поля, чужие изменения которых показываются при конфликте: редактируемые, статус и спринт. */
export const CONFLICT_FIELDS = ["title", "status", ...EDITABLE_FIELDS.slice(1), "sprint_id"] as const;
export type ConflictField = (typeof CONFLICT_FIELDS)[number];

/** Заголовок If-Match: сильный ETag — версия в кавычках, как сервер отдаёт в ETag. */
export function ifMatch(version: number | undefined): Record<string, string> {
  return { "If-Match": `"${version ?? 0}"` };
}

/** Значение поля в сравнимом виде: пустое (undefined, "", пробелы, []) — null. */
function norm(field: string, v: unknown): unknown {
  if (v === undefined || v === null) return null;
  if (typeof v === "string") {
    const t = field === "description" ? (v.trim() ? v : "") : v.trim();
    return t === "" ? null : t;
  }
  if (Array.isArray(v)) return v.length ? v : null;
  return v;
}

export function sameValue(field: string, a: unknown, b: unknown): boolean {
  const x = norm(field, a);
  const y = norm(field, b);
  if (Array.isArray(x) && Array.isArray(y)) return x.length === y.length && x.every((v, i) => v === y[i]);
  return x === y;
}

/**
 * Тело PATCH: только поля, отличающиеся от `base`; очищенное поле — null (метки — пустой
 * массив). Пустой объект — менять нечего.
 */
export function buildPatch(base: TaskTask, edits: Edits): TaskUpdateRequest {
  const patch: Record<string, unknown> = {};
  for (const f of EDITABLE_FIELDS) {
    if (!(f in edits)) continue;
    const v = edits[f];
    if (sameValue(f, base[f], v)) continue;
    const n = norm(f, v);
    patch[f] = f === "labels" ? (n ?? []) : n;
  }
  return patch as TaskUpdateRequest;
}

export function isEmptyPatch(p: TaskUpdateRequest): boolean {
  return Object.keys(p).length === 0;
}

/** Актуальная задача из ответа 409 VERSION_CONFLICT (details.current); иначе null. */
export function versionConflict(e: unknown): TaskTask | null {
  const err = parseApiError(e);
  if (err?.status !== 409 || err.code !== "VERSION_CONFLICT") return null;
  const cur = err.details.current;
  return cur && typeof cur === "object" && !Array.isArray(cur) ? (cur as TaskTask) : null;
}

/** Строка сравнения при конфликте. */
export interface ConflictRow {
  field: ConflictField;
  /** Значение, от которого шла правка (версия, открытая у пользователя). */
  before: unknown;
  /** Значение в актуальной версии. */
  theirs: unknown;
  /** Изменено другим пользователем (before ≠ theirs). */
  changedByOthers: boolean;
  /** Своё новое значение, если поле в нашей правке. */
  mine?: unknown;
  inMine: boolean;
  /** Одно поле поменяли оба, и по-разному: повтор перезапишет чужое значение. */
  clash: boolean;
}

/**
 * Сравнение при конфликте: что изменил другой (base → current) и что меняем мы (patch).
 * В список попадают поля, изменённые другим, и поля нашей правки.
 */
export function compareConflict(base: TaskTask, current: TaskTask, patch: TaskUpdateRequest): ConflictRow[] {
  const mine = patch as Record<string, unknown>;
  const rows: ConflictRow[] = [];
  for (const f of CONFLICT_FIELDS) {
    const changedByOthers = !sameValue(f, base[f], current[f]);
    const inMine = f in mine;
    if (!changedByOthers && !inMine) continue;
    rows.push({
      field: f,
      before: base[f],
      theirs: current[f],
      changedByOthers,
      mine: inMine ? mine[f] : undefined,
      inMine,
      clash: changedByOthers && inMine && !sameValue(f, current[f], mine[f]),
    });
  }
  return rows;
}

/**
 * Свои изменения поверх актуальной версии: поля, которые там уже такие же, отпадают.
 * Пустой результат — повторять нечего.
 */
export function rebasePatch(current: TaskTask, patch: TaskUpdateRequest): TaskUpdateRequest {
  return buildPatch(current, patch as Edits);
}

/** Строка меток «api, вход ,  ui» → ["api", "вход", "ui"] без пустых и повторов. */
export function parseLabels(input: string): string[] {
  const out: string[] = [];
  for (const raw of input.split(",")) {
    const l = raw.trim();
    if (l && !out.includes(l)) out.push(l);
  }
  return out;
}

/**
 * От какой версии сохранять правку, начатую на версии `opened`: если все версии после неё
 * получены нашими же изменениями (`own`), то от текущей — иначе от открытой, чтобы чужая
 * правка дала 409, а не перезаписалась.
 */
export function ifMatchBase(opened: TaskTask, current: TaskTask | undefined, own: ReadonlySet<number>): TaskTask {
  const from = opened.version ?? 0;
  const to = current?.version ?? from;
  if (!current || to <= from) return opened;
  for (let v = from + 1; v <= to; v++) if (!own.has(v)) return opened;
  return current;
}
