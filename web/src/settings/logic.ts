// Логика настроек проекта (ТЗ §7.9): какие вкладки видны, тексты ошибок, события §8.6, журнал §3.8.
// Права из §2 здесь только скрывают вкладки и кнопки — проверяет сервер.
import type { AuditEntry, TaskTrashItem } from "../api/client";
import { errorText, parseApiError } from "../api/errors";

export type SettingsTab = "general" | "members" | "telegram" | "github" | "notifications" | "audit" | "trash";

export const SETTINGS_TABS: { id: SettingsTab; label: string; adminOnly: boolean }[] = [
  { id: "general", label: "Общее", adminOnly: false },
  { id: "members", label: "Участники", adminOnly: false },
  { id: "telegram", label: "Telegram", adminOnly: false },
  { id: "github", label: "GitHub", adminOnly: false },
  { id: "notifications", label: "Уведомления", adminOnly: false },
  { id: "audit", label: "Журнал", adminOnly: true },
  { id: "trash", label: "Корзина", adminOnly: true },
];

/**
 * Вкладки для пользователя: настройки доступны менеджеру и администратору системы (§7.9);
 * Журнал и Корзина — только администратору (§2), менеджер их не видит. Исполнителю — ничего.
 * «GitHub» — только если интеграция подключена (§12.1, github_enabled в GET /me).
 */
export function visibleTabs(opts: { manager: boolean; admin: boolean; github?: boolean }): SettingsTab[] {
  if (!opts.manager && !opts.admin) return [];
  return SETTINGS_TABS.filter((t) => (!t.adminOnly || opts.admin) && (t.id !== "github" || opts.github)).map((t) => t.id);
}

/** Вкладка из адреса (?tab=…); недоступная или неизвестная — первая доступная. */
export function tabFromParam(value: string | null, visible: SettingsTab[]): SettingsTab | null {
  if (visible.length === 0) return null;
  return visible.includes(value as SettingsTab) ? (value as SettingsTab) : visible[0];
}

/** Что делали с участником — от этого зависит текст ошибки LAST_MANAGER (§9.3). */
export type MemberAction = "role" | "remove" | "deactivate";

/**
 * Текст ошибки действия с участником. LAST_MANAGER объясняем по-человечески и говорим, что
 * делать дальше; при деактивации сервер перечисляет проекты в details.projects.
 */
export function memberErrorText(e: unknown, action: MemberAction): string {
  const err = parseApiError(e);
  if (err?.code !== "LAST_MANAGER") return errorText(e);
  if (action === "deactivate") {
    const projects = Array.isArray(err.details.projects) ? (err.details.projects as unknown[]).map(String) : [];
    const where = projects.length > 0 ? ` (${projects.join(", ")})` : "";
    return `Нельзя деактивировать: это единственный менеджер проекта${where}. Сначала назначьте менеджером другого участника.`;
  }
  const what = action === "role" ? "понизить" : "исключить";
  return `Нельзя ${what} единственного менеджера проекта. Сначала назначьте менеджером другого участника.`;
}

/** События бота в группе (§8.6) — ключи notification_settings из API и умолчания из таблицы. */
export const GROUP_EVENTS: { key: string; label: string; byDefault: boolean }[] = [
  { key: "task_created", label: "Создана задача", byDefault: true },
  { key: "task_taken", label: "Задача взята в работу или назначена", byDefault: true },
  { key: "task_returned", label: "Задача возвращена в очередь", byDefault: true },
  { key: "task_review", label: "Задача отправлена на проверку", byDefault: true },
  { key: "task_rework", label: "Задача возвращена на доработку", byDefault: true },
  { key: "task_accepted", label: "Задача принята", byDefault: true },
  { key: "task_cancelled", label: "Задача отменена", byDefault: true },
  { key: "sprint_started", label: "Спринт запущен", byDefault: true },
  { key: "sprint_closed", label: "Спринт завершён", byDefault: true },
  { key: "daily_summary", label: "Ежедневная сводка", byDefault: false },
  { key: "comment_created", label: "Новый комментарий", byDefault: false },
];

/** Включённые события: сохранённое значение, иначе умолчание §8.6. */
export function effectiveEvents(stored: Record<string, boolean> | undefined): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const e of GROUP_EVENTS) out[e.key] = stored?.[e.key] ?? e.byDefault;
  return out;
}

/** Только изменённые переключатели — PATCH принимает частичный объект. */
export function changedEvents(before: Record<string, boolean>, after: Record<string, boolean>): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  for (const e of GROUP_EVENTS) if (before[e.key] !== after[e.key]) out[e.key] = after[e.key];
  return out;
}

/** Время сводки «ЧЧ:ММ»: сервер может отдать с секундами. */
export function normalizeTime(v: string | undefined): string {
  const m = /^(\d{1,2}):(\d{2})/.exec(v ?? "");
  return m ? `${m[1].padStart(2, "0")}:${m[2]}` : "";
}

export function isValidTime(v: string): boolean {
  return /^([01]\d|2[0-3]):[0-5]\d$/.test(v);
}

/**
 * Типы действий журнала проекта (api/internal/audit) — для фильтра и подписи строки. user.*
 * (создание, изменение, деактивация пользователя) пишутся без проекта и в журнал проекта не
 * попадают — в фильтре их нет.
 */
export const AUDIT_ACTIONS: { value: string; label: string }[] = [
  { value: "project.created", label: "Проект создан" },
  { value: "project.updated", label: "Настройки проекта изменены" },
  { value: "project.archived", label: "Проект архивирован" },
  { value: "project.unarchived", label: "Проект возвращён из архива" },
  { value: "project.deleted", label: "Проект удалён" },
  { value: "project.restored", label: "Проект восстановлен" },
  { value: "project.telegram_bound", label: "Telegram-группа подключена" },
  { value: "project.telegram_unbound", label: "Telegram-группа отключена" },
  { value: "member.added", label: "Участник добавлен" },
  { value: "member.role_changed", label: "Роль изменена" },
  { value: "member.removed", label: "Участник исключён" },
  { value: "invite.created", label: "Ссылка-приглашение создана" },
  { value: "invite.revoked", label: "Ссылка-приглашение отозвана" },
  { value: "task.deleted", label: "Задача удалена" },
  { value: "task.restored", label: "Задача восстановлена" },
  { value: "worklog.updated", label: "Чужая запись времени изменена" },
  { value: "worklog.deleted", label: "Чужая запись времени удалена" },
];

export function auditActionLabel(action: string | undefined): string {
  return AUDIT_ACTIONS.find((a) => a.value === action)?.label ?? action ?? "";
}

const SOURCES: Record<string, string> = { web: "веб", bot: "бот", cli: "консоль", system: "система" };

export function auditSourceLabel(source: string | undefined): string {
  return SOURCES[source ?? ""] ?? source ?? "";
}

function show(v: unknown): string {
  if (v === null || v === undefined || v === "") return "—";
  if (typeof v === "boolean") return v ? "да" : "нет";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

/**
 * Что изменилось в записи журнала: «поле: было → стало» по каждому полю из old_value и
 * new_value. Запись без старого значения (создание) — «поле: значение».
 */
export function auditChanges(e: Pick<AuditEntry, "old_value" | "new_value">): string[] {
  const oldV = (e.old_value ?? {}) as Record<string, unknown>;
  const newV = (e.new_value ?? {}) as Record<string, unknown>;
  const fields = [...new Set([...Object.keys(oldV), ...Object.keys(newV)])];
  return fields.map((f) => {
    if (!(f in oldV)) return `${f}: ${show(newV[f])}`;
    if (!(f in newV)) return `${f}: ${show(oldV[f])} → —`;
    return `${f}: ${show(oldV[f])} → ${show(newV[f])}`;
  });
}

/**
 * Над чем действие: задача — её ключ, участник — имя пользователя, запись времени — задача и
 * чья запись; проект — пусто (журнал и так по проекту).
 */
export function auditEntity(
  e: Pick<AuditEntry, "entity_type" | "entity_id" | "old_value" | "new_value">,
  nameOf: (userId: string) => string,
): string {
  const v = { ...((e.old_value ?? {}) as Record<string, unknown>), ...((e.new_value ?? {}) as Record<string, unknown>) };
  switch (e.entity_type) {
    case "task":
      return String(v.key ?? e.entity_id ?? "");
    case "user":
      return e.entity_id ? nameOf(e.entity_id) : "";
    case "worklog": {
      const who = typeof v.user_id === "string" ? nameOf(v.user_id) : "";
      return [v.task_key, who].filter(Boolean).join(", ");
    }
    default:
      return "";
  }
}

/** Запрос журнала из фильтров формы: пустые поля не передаём. */
export function auditQuery(f: { from: string; to: string; userId: string; action: string }) {
  const q: { from?: string; to?: string; user_id?: string; action?: string } = {};
  if (f.from) q.from = f.from;
  if (f.to) q.to = f.to;
  if (f.userId) q.user_id = f.userId;
  if (f.action) q.action = f.action;
  return q;
}

/** Корзина: кто удалил — имя из ответа GET /projects/{key}/trash; неизвестен — прочерк. */
export function trashDeleter(t: Pick<TaskTrashItem, "deleted_by">): string {
  return t.deleted_by?.display_name || "—";
}

/** Код подключения группы (§8.2) с обязательными полями. */
export interface GroupBindCode {
  code: string;
  expires_at: string;
  command: string;
  bot_username: string;
}

/** Ответ сервера → код подключения (в Swagger поля ответа необязательные). */
export function bindCodeFrom(d: Partial<GroupBindCode>): GroupBindCode {
  const code = d.code ?? "";
  return { code, expires_at: d.expires_at ?? "", command: d.command || `/bind ${code}`, bot_username: d.bot_username ?? "" };
}

/** Подключённая группа для вкладки «Telegram»: название, а если его нет — id чата; null — не подключена. */
export function groupLabel(p: { telegram_chat_id?: number; telegram_chat_title?: string }): string | null {
  if (!p.telegram_chat_id) return null;
  return p.telegram_chat_title ? `«${p.telegram_chat_title}»` : `id чата ${p.telegram_chat_id}`;
}

export type Role = "manager" | "member";

/** Роли участника проекта (§2) — для списков участников и ссылок-приглашений. */
export const ROLES: { value: Role; label: string }[] = [
  { value: "member", label: "Исполнитель" },
  { value: "manager", label: "Менеджер" },
];

export function roleLabel(role: string | undefined): string {
  return ROLES.find((r) => r.value === role)?.label ?? role ?? "";
}
