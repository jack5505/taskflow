// Логика страницы «Проекты» (ТЗ §7.2), отделённая от разметки ради тестов.
import type { MeProfile } from "../api/client";

// Ключи кэша TanStack Query: список проектов (с фильтром в хвосте) и «Мои задачи».
export const projectsQueryKey = ["projects"] as const;
export const myTasksQueryKey = ["my-tasks"] as const;

/** Часовой пояс проекта по умолчанию (§3.2). */
export const DEFAULT_TIMEZONE = "Europe/Moscow";

/** Какие проекты показывать: обычные, «Архив» или «Удалённые» (только администратор). */
export type ProjectsFilter = "active" | "archived" | "deleted";

/** Кнопки «Создать проект», фильтр «Удалённые» и «Восстановить» — только администратору системы (§2). */
export function isSystemAdmin(me: MeProfile | undefined): boolean {
  return me?.is_system_admin === true;
}

/**
 * Фильтр из адреса (?archived=true, ?deleted=true — как в GET /projects). «Удалённые» не
 * администратору сервер отклонит с 403, поэтому такой адрес показывает обычный список.
 */
export function filterFromParams(params: URLSearchParams, admin: boolean): ProjectsFilter {
  if (admin && params.get("deleted") === "true") return "deleted";
  if (params.get("archived") === "true") return "archived";
  return "active";
}

/** Адресная строка для фильтра: обычный список — без параметров. */
export function paramsForFilter(f: ProjectsFilter): Record<string, string> {
  if (f === "archived") return { archived: "true" };
  if (f === "deleted") return { deleted: "true" };
  return {};
}

/** Параметры запроса GET /projects для фильтра. */
export function listQuery(f: ProjectsFilter): { archived?: boolean; deleted?: boolean } {
  if (f === "archived") return { archived: true };
  if (f === "deleted") return { deleted: true };
  return {};
}

/** Фильтры, которые видит пользователь. */
export function visibleFilters(admin: boolean): { value: ProjectsFilter; label: string }[] {
  const list: { value: ProjectsFilter; label: string }[] = [
    { value: "active", label: "Проекты" },
    { value: "archived", label: "Архив" },
  ];
  if (admin) list.push({ value: "deleted", label: "Удалённые" });
  return list;
}

const KEY_RE = /^[A-Z]{2,10}$/;

/** Ключ проекта (§3.2): 2–10 заглавных латинских букв. Возвращает причину отказа, "" — ключ годится. */
export function projectKeyError(key: string): string {
  return KEY_RE.test(key) ? "" : "От 2 до 10 заглавных латинских букв";
}

/** Ввод ключа: заглавные буквы, пробелы отбрасываем. */
export function normalizeKeyInput(value: string): string {
  return value.toUpperCase().replace(/\s+/g, "");
}

/** Часовые пояса IANA для подсказки в форме; старый браузер без списка — только пояс по умолчанию. */
export function timeZones(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return [DEFAULT_TIMEZONE];
  }
}
