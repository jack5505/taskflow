// Уведомления колокольчика (ТЗ §7.7): тексты по типам и правка кэша, отделённые от разметки ради тестов.
import type { InfiniteData } from "@tanstack/react-query";
import type { NotificationNotification, NotificationPage } from "../api/client";
import { formatDuration } from "../lib/duration";
import { formatDate } from "../lib/format";
import { statusLabel } from "../lib/status";

/** Общий префикс: инвалидация по нему обновляет и счётчик, и список. */
export const notificationsQueryKey = ["notifications"] as const;
/** Счётчик на колокольчике: первая страница из одного элемента, unread_count приходит с ней. */
export const unreadQueryKey = [...notificationsQueryKey, "unread"] as const;
/** Список в открытом колокольчике, по курсору. */
export const listQueryKey = [...notificationsQueryKey, "list"] as const;

export interface NotificationText {
  text: string;
  /** Пояснение второй строкой: начало комментария, сколько списано. */
  detail?: string;
  /** Куда ведёт уведомление: задача или проект; нет — задачи или доступа к проекту уже нет. */
  link?: string;
}

type Payload = Record<string, unknown>;

const str = (p: Payload, k: string) => (typeof p[k] === "string" ? (p[k] as string) : "");
const num = (p: Payload, k: string) => (typeof p[k] === "number" ? (p[k] as number) : 0);

/** «TM-42 «Название»»; без названия — только ключ. */
function taskRef(p: Payload): string {
  const key = str(p, "key");
  const title = str(p, "title");
  return title ? `${key} «${title}»` : key;
}

const roleNames: Record<string, string> = { manager: "менеджер", member: "участник" };

/** Комментарий в уведомлении — простым текстом, одной строкой, не длиннее limit символов. */
export function snippet(body: string, limit = 140): string {
  const flat = body.replace(/\s+/g, " ").trim();
  const chars = [...flat];
  return chars.length > limit ? `${chars.slice(0, limit - 1).join("")}…` : flat;
}

/** Русский текст уведомления по типу и полям события (payload). */
export function describeNotification(n: NotificationNotification): NotificationText {
  const p = (n.payload ?? {}) as Payload;
  const key = str(p, "key");
  const taskLink = key ? `/tasks/${key}` : undefined;
  switch (n.type) {
    case "comment":
      return { text: `Новый комментарий к задаче ${taskRef(p)}`, detail: snippet(str(p, "body")) || undefined, link: taskLink };
    case "assigned":
      return { text: `Вас назначили исполнителем задачи ${taskRef(p)}`, link: taskLink };
    case "status":
      return {
        text: `Задача ${taskRef(p)}: ${statusLabel(str(p, "from"))} → ${statusLabel(str(p, "to"))}`,
        detail: snippet(str(p, "comment")) || undefined,
        link: taskLink,
      };
    case "rework":
      return {
        text: `Задачу ${taskRef(p)} вернули на доработку`,
        detail: snippet(str(p, "comment")) || undefined,
        link: taskLink,
      };
    case "review_requested":
      return { text: `Задача ${taskRef(p)} отправлена на проверку`, link: taskLink };
    case "user_deactivated": {
      const projects = Array.isArray(p.projects) ? (p.projects as Payload[]) : [];
      const tasks = projects.flatMap((pr) =>
        Array.isArray(pr.released_tasks) ? (pr.released_tasks as unknown[]).filter((k) => typeof k === "string") : [],
      );
      const first = projects[0] ? str(projects[0], "project_key") : "";
      return {
        text: `Пользователь ${str(p, "display_name")} деактивирован`,
        detail: tasks.length ? `Освобождены задачи: ${tasks.join(", ")}` : undefined,
        link: first ? `/projects/${first}` : undefined,
      };
    }
    case "task_deleted":
      // Удалённую задачу видит только администратор системы — ссылку не даём.
      return { text: `Задача ${taskRef(p)} удалена` };
    case "member_added": {
      const project = str(p, "project_key");
      const role = roleNames[str(p, "role")];
      return {
        text: `Вас добавили в проект ${project}${role ? ` с ролью «${role}»` : ""}`,
        link: project ? `/projects/${project}` : undefined,
      };
    }
    case "member_removed":
      return { text: `Вас исключили из проекта ${str(p, "project_key")}` };
    case "timer_auto_stopped": {
      const minutes = num(p, "minutes");
      return {
        text: `Таймер на задаче ${taskRef(p)} остановлен автоматически через 12 часов`,
        detail: minutes > 0 ? `Списано ${formatDuration(minutes)}` : "Время не списано",
        link: taskLink,
      };
    }
    case "worklog_trimmed": {
      const written = num(p, "written_minutes");
      const requested = formatDuration(num(p, "requested_minutes"));
      const date = formatDate(str(p, "work_date"));
      return written > 0
        ? {
            text: `Запись таймера по задаче ${taskRef(p)} обрезана по дневному лимиту 24h`,
            detail: `За ${date}: списано ${formatDuration(written)} из ${requested}`,
            link: taskLink,
          }
        : {
            text: `Время таймера по задаче ${taskRef(p)} не списано: дневной лимит 24h исчерпан`,
            detail: `За ${date}: не записано ${requested}`,
            link: taskLink,
          };
    }
    default:
      return { text: key ? `Событие по задаче ${taskRef(p)}` : "Новое событие", link: taskLink };
  }
}

export type NotificationsData = InfiniteData<NotificationPage, string>;

/**
 * Отметить прочитанными в кэше до ответа сервера: ids — выбранные, null — все. Счётчик
 * непрочитанных (он только на первой странице) уменьшается на число реально отмеченных.
 */
export function markReadInCache(
  data: NotificationsData | undefined,
  ids: string[] | null,
  readAt: string,
): NotificationsData | undefined {
  if (!data) return data;
  const pick = ids && new Set(ids);
  let marked = 0;
  const pages = data.pages.map((page) => ({
    ...page,
    items: page.items?.map((n) => {
      if (n.read_at || (pick && !pick.has(n.id ?? ""))) return n;
      marked++;
      return { ...n, read_at: readAt };
    }),
  }));
  const first = pages[0];
  if (first && first.unread_count !== undefined)
    first.unread_count = pick ? Math.max(0, first.unread_count - marked) : 0;
  return { ...data, pages };
}

/** Счётчик на колокольчике: «99+» сверх 99, ноль — без счётчика. */
export function badgeText(unread: number | undefined): string {
  if (!unread || unread < 0) return "";
  return unread > 99 ? "99+" : String(unread);
}
