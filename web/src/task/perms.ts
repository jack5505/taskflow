// Что показывать в карточке задачи (§2, §4). Только скрываем кнопки — проверяет сервер.
import type { TaskTask } from "../api/client";
import type { Actor } from "../board/transitions";

export interface TaskPerms {
  /** Править поля: автор, исполнитель или менеджер. */
  edit: boolean;
  /** Выбирать исполнителя: менеджер, задача не закрыта. */
  assign: boolean;
  /** Снять исполнителя: нельзя с задачи в работе (сначала в «К выполнению»). */
  unassign: boolean;
  /** Оценку задачи, которая была в работе, меняет только менеджер. */
  estimate: boolean;
  /** «Взять в работу». */
  take: boolean;
  /** Писать комментарии. */
  comment: boolean;
  /** Удалить задачу: менеджер. */
  remove: boolean;
  /** Восстановить удалённую: администратор системы. */
  restore: boolean;
}

const none: TaskPerms = {
  edit: false,
  assign: false,
  unassign: false,
  estimate: false,
  take: false,
  comment: false,
  remove: false,
  restore: false,
};

/**
 * Права в карточке. archived — проект в архиве (всё только для чтения, §3.2), admin —
 * администратор системы.
 */
export function taskPerms(t: TaskTask, actor: Actor | null, opts: { archived: boolean; admin: boolean }): TaskPerms {
  if (!actor || opts.archived) return none;
  if (t.deleted_at) return { ...none, restore: opts.admin };
  const mine = !!t.assignee_id && t.assignee_id === actor.userId;
  const reporter = !!t.reporter_id && t.reporter_id === actor.userId;
  const closed = t.status === "done" || t.status === "cancelled";
  const worked = !!t.started_at || t.status === "in_progress" || t.status === "review" || t.status === "done";
  const edit = actor.manager || mine || reporter;
  const assign = actor.manager && !closed;
  return {
    edit,
    assign,
    unassign: assign && t.status !== "in_progress",
    estimate: edit && (actor.manager || !worked),
    take:
      actor.member &&
      ((t.status === "todo" && (!t.assignee_id || mine)) || (t.status === "in_progress" && !t.assignee_id)),
    comment: actor.member || actor.manager,
    remove: actor.manager,
    restore: false,
  };
}

/** Комментарий правит и удаляет автор или менеджер (§3.6). */
export function canManageComment(authorId: string | undefined, actor: Actor | null, archived: boolean): boolean {
  if (!actor || archived) return false;
  return actor.manager || (!!authorId && authorId === actor.userId);
}

/** Вложение удаляет автор файла или менеджер (§3.9) — то же правило, что для комментария. */
export const canManageAttachment = canManageComment;

/** Лимит вложения (§3.9). */
export const MAX_ATTACHMENT_BYTES = 50 * 1024 * 1024;

/** Картинка — показываем превью; остальное только скачивание (§7.5). */
export function isPreviewable(mime: string | undefined): boolean {
  return /^image\/(png|jpe?g|gif|webp|bmp|avif)$/i.test(mime ?? "");
}
