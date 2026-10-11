// Логика страницы «Отчёты» (ТЗ §6.6), отделённая от разметки ради тестов.
// Фильтры живут в адресе: ?from=2026-10-05&to=2026-10-11&project=TM&sprint=<uuid>&user=<uuid>&view=task
import type { MeProfile } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";

/** Представления отчёта: по пользователям и по задачам. */
export type ReportView = "user" | "task";

export const REPORT_VIEWS: { id: ReportView; label: string }[] = [
  { id: "user", label: "По пользователям" },
  { id: "task", label: "По задачам" },
];

export interface ReportFilter {
  /** ГГГГ-ММ-ДД; пусто — граница по умолчанию (текущая неделя по поясу пользователя считает сервер). */
  from: string;
  to: string;
  /** Ключ проекта. */
  project: string;
  /** id спринта; без проекта не бывает. */
  sprint: string;
  /** id пользователя. */
  user: string;
  view: ReportView;
}

export const reportQueryKey = ["report-time"] as const;

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
const KEY_RE = /^[A-Z]{2,10}$/;
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Фильтр из адреса. Мусор отбрасываем — иначе сервер ответит 400 на ровном месте. */
export function filterFromParams(params: URLSearchParams): ReportFilter {
  const get = (name: string, re: RegExp) => {
    const v = params.get(name) ?? "";
    return re.test(v) ? v : "";
  };
  const project = get("project", KEY_RE);
  return {
    from: get("from", DATE_RE),
    to: get("to", DATE_RE),
    project,
    // Спринт выбирается внутри проекта: без проекта его не показать в списке.
    sprint: project ? get("sprint", UUID_RE) : "",
    user: get("user", UUID_RE),
    view: params.get("view") === "task" ? "task" : "user",
  };
}

/** Адрес для фильтра; пустые поля и представление по умолчанию в адрес не попадают. */
export function paramsFromFilter(f: ReportFilter): URLSearchParams {
  const p = new URLSearchParams();
  if (f.from) p.set("from", f.from);
  if (f.to) p.set("to", f.to);
  if (f.project) p.set("project", f.project);
  if (f.sprint) p.set("sprint", f.sprint);
  if (f.user) p.set("user", f.user);
  if (f.view !== "user") p.set("view", f.view);
  return p;
}

/**
 * Смена проекта: спринт принадлежал прежнему проекту, а выбранный пользователь может не быть
 * участником нового — оба сбрасываются.
 */
export function withProject(f: ReportFilter, project: string): ReportFilter {
  return { ...f, project, sprint: "", user: "" };
}

/** Проект в фильтре: ключ, название и роль пользователя в нём. */
export interface ProjectOption {
  key: string;
  name: string;
  /** manager | member; у администратора системы без участия — пусто. */
  role: string;
}

/**
 * Проекты для фильтра: где пользователь участник (GET /me), администратору системы — ещё и все
 * остальные (GET /projects). Порядок — по ключу.
 */
export function projectOptions(
  me: MeProfile | undefined,
  all: { key?: string; name?: string }[] = [],
): ProjectOption[] {
  const byKey = new Map<string, ProjectOption>();
  for (const p of me?.projects ?? []) {
    if (p.key) byKey.set(p.key, { key: p.key, name: p.name ?? "", role: p.role ?? "" });
  }
  if (me?.is_system_admin) {
    for (const p of all) {
      if (p.key && !byKey.has(p.key)) byKey.set(p.key, { key: p.key, name: p.name ?? "", role: "" });
    }
  }
  return [...byKey.values()].sort((a, b) => a.key.localeCompare(b.key));
}

/**
 * Фильтр «пользователь» заблокирован (§2, §6.6): исполнитель видит отчёты только по себе.
 * В выбранном проекте решает роль в нём; без проекта — есть ли хоть один проект, где пользователь
 * менеджер. Администратор системы видит всех.
 */
export function userLocked(me: MeProfile | undefined, projects: ProjectOption[], project: string): boolean {
  if (!me || me.is_system_admin) return false;
  if (project) return projects.find((p) => p.key === project)?.role !== "manager";
  return !projects.some((p) => p.role === "manager");
}

/** Параметры GET /reports/time. Заблокированный фильтр — всегда сам пользователь. */
export function reportQuery(f: ReportFilter, lockedTo: string | null) {
  const user = lockedTo ?? f.user;
  return {
    from: f.from || undefined,
    to: f.to || undefined,
    project: f.project || undefined,
    sprint_id: f.sprint || undefined,
    user_id: user || undefined,
    group_by: f.view,
  };
}

/** Минуты в десятичных часах с двумя знаками (§6.1): 150 → «2.50». */
export function formatHours(minutes: number | null | undefined): string {
  return ((minutes ?? 0) / 60).toFixed(2);
}

const weekdays = ["вс", "пн", "вт", "ср", "чт", "пт", "сб"];

/** День недели для заголовка столбца: «пн». Дата календарная, пояс к ней не применяется. */
export function weekday(date: string): string {
  const m = DATE_RE.test(date) ? date.split("-").map(Number) : null;
  if (!m) return "";
  return weekdays[new Date(Date.UTC(m[0]!, m[1]! - 1, m[2]!)).getUTCDay()]!;
}

/**
 * Новый период после правки одной границы. Вторая остаётся, а если оказалась по другую сторону —
 * сдвигается к правленой (период в один день): иначе сервер отклонил бы «по» раньше «с».
 * Даты ГГГГ-ММ-ДД сравниваются как строки.
 */
export function withPeriod(f: ReportFilter, from: string, to: string, changed: "from" | "to"): ReportFilter {
  if (from && to && from > to) {
    if (changed === "from") to = from;
    else from = to;
  }
  return { ...f, from, to };
}

const fieldNames: Record<string, string> = {
  from: "Дата «с»",
  to: "Дата «по»",
  group_by: "Представление",
  sprint_id: "Спринт",
  user_id: "Пользователь",
  project: "Проект",
};

/** Понятный текст ошибки отчёта: неверный период, чужой проект, отчёт по другим без прав. */
export function reportErrorText(e: unknown): string {
  const err = parseApiError(e);
  if (err?.status === 400) {
    const fields = fieldErrors(err);
    if (fields.length > 0) {
      return fields.map((f) => `${fieldNames[f.field] ?? f.field}: ${f.reason}`).join("; ");
    }
  }
  if (err?.status === 403) return err.message || "Отчёт по другим пользователям доступен менеджеру проекта";
  if (err?.status === 404) return err.message || "Проект или спринт не найден";
  return errorText(e, "Не удалось построить отчёт, попробуйте ещё раз");
}

/**
 * Ответ с ошибкой при скачивании CSV: клиент разобрал тело как файл, а не как JSON, и текст
 * ошибки §9.3 лежит внутри Blob. Достаём его, чтобы показать как обычную ошибку API.
 */
export async function unwrapBlobError(e: unknown): Promise<unknown> {
  if (!(e instanceof Response)) return e;
  const r = e as Response & { error?: unknown };
  if (r.error instanceof Blob) {
    try {
      r.error = JSON.parse(await r.error.text());
    } catch {
      r.error = null;
    }
  }
  return r;
}

/** Имя файла из Content-Disposition (filename*= или filename=), иначе по периоду и представлению. */
export function csvFileName(disposition: string | null, f: { from?: string; to?: string; view: ReportView }): string {
  if (disposition) {
    const star = /filename\*\s*=\s*(?:UTF-8|utf-8)''([^;]+)/.exec(disposition);
    if (star) {
      try {
        return decodeURIComponent(star[1]!.trim());
      } catch {
        // битая кодировка — пробуем обычное имя
      }
    }
    const plain = /filename\s*=\s*"?([^";]+)"?/.exec(disposition);
    if (plain) return plain[1]!.trim();
  }
  // Как у сервера: time-report-user-2026-10-05_2026-10-11.csv.
  const period = f.from && f.to ? `-${f.from}_${f.to}` : "";
  return `time-report-${f.view}${period}.csv`;
}
