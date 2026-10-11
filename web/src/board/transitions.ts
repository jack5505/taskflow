// Переходы статусов и права на них (ТЗ §4, §2) — для подсветки колонок доски при перетаскивании.
// Интерфейс только скрывает недоступное; окончательную проверку делает сервер (web/CLAUDE.md).

export type Status = "todo" | "in_progress" | "review" | "done" | "cancelled";

/** Колонки доски строго по статусам (§7.3); отменённые на доске не показываются. */
export const BOARD_COLUMNS = ["todo", "in_progress", "review", "done"] as const;
export type BoardStatus = (typeof BOARD_COLUMNS)[number];

/** Схема переходов §4; любой другой переход сервер отклонит с 409 INVALID_TRANSITION. */
const ALLOWED: Record<Status, readonly Status[]> = {
  todo: ["in_progress", "cancelled"],
  in_progress: ["todo", "review", "cancelled"],
  review: ["in_progress", "done", "cancelled"],
  done: ["in_progress"],
  cancelled: ["todo"],
};

/** Кто действует: менеджер проекта или администратор системы (§2) и участник ли он проекта. */
export interface Actor {
  userId: string;
  manager: boolean;
  member: boolean;
}

export interface TaskRef {
  status: Status;
  assigneeId?: string | null;
  reporterId?: string | null;
}

export function isValidTransition(from: Status, to: Status): boolean {
  return ALLOWED[from]?.includes(to) ?? false;
}

/** Разрешён ли переход задачи в статус `to` этому пользователю (таблица §4). */
export function canTransition(task: TaskRef, to: Status, actor: Actor): boolean {
  const from = task.status;
  if (!isValidTransition(from, to)) return false;
  const assignee = !!task.assigneeId && task.assigneeId === actor.userId;
  const reporter = !!task.reporterId && task.reporterId === actor.userId;

  if (from === "todo" && to === "in_progress") {
    // Свободную берёт любой участник (и становится исполнителем); назначенную на другого —
    // только исполнитель и менеджер.
    if (!task.assigneeId) return actor.member;
    return assignee || actor.manager;
  }
  if (from === "in_progress" && (to === "todo" || to === "review")) return assignee || actor.manager;
  if (from === "review" && to === "in_progress") return reporter || actor.manager;
  // Принять свою задачу, где он и автор, и исполнитель, может только менеджер.
  if (from === "review" && to === "done") return actor.manager || (reporter && !assignee);
  // Переоткрыть, отменить и вернуть в бэклог — только менеджер.
  return actor.manager;
}

/**
 * Какой комментарий нужен переходу: причина возврата на доработку (не короче 5 символов)
 * или причина отмены (обязательна); null — без комментария.
 */
export function commentRequirement(from: Status, to: Status): "rework" | "cancel" | null {
  if (from === "review" && to === "in_progress") return "rework";
  if (to === "cancelled" && from !== "cancelled") return "cancel";
  return null;
}

export const MIN_REWORK_COMMENT = 5;

/** Проверка комментария перед отправкой; "" — годится. Длину считаем по символам, как сервер. */
export function commentError(kind: "rework" | "cancel", comment: string): string {
  const len = [...comment.trim()].length;
  if (kind === "rework" && len < MIN_REWORK_COMMENT) return "Причина возврата обязательна, не короче 5 символов";
  if (kind === "cancel" && len === 0) return "Причина отмены обязательна";
  return "";
}

/**
 * Переоткрытие задачи, чей исполнитель деактивирован: менеджер обязан выбрать активного (§4).
 * assigneeActive — признак из списка участников; undefined (не знаем) — не спрашиваем,
 * тогда об этом скажет сервер.
 */
export function needsNewAssignee(from: Status, to: Status, assigneeActive: boolean | undefined): boolean {
  return from === "done" && to === "in_progress" && assigneeActive === false;
}
