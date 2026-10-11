import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api } from "../api";
import type { AuditEntry, TaskListItem } from "../api/client";
import { errorText } from "../api/errors";
import { useTimeZone } from "../auth/hooks";
import { boardQueryKey } from "../board/cards";
import { formatDateTime } from "../lib/format";
import { myTasksQueryKey } from "../projects/logic";
import { useUsers, userName } from "./hooks";
import { trashItems } from "./logic";

/** Все страницы списка: сервер отдаёт до 200 за раз, остальное — по next_cursor. */
async function fetchAll<T>(load: (cursor?: string) => Promise<{ items?: T[]; next_cursor?: string }>): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  do {
    const page = await load(cursor);
    items.push(...(page.items ?? []));
    cursor = page.next_cursor || undefined;
  } while (cursor);
  return items;
}

/**
 * Корзина (§7.9, только администратор системы): удалённые задачи проекта — ключ, название, кто
 * и когда удалил, «Восстановить». Отдельного списка удалённых в API нет: берём задачи с
 * include_deleted и оставляем удалённые; кто удалил — из журнала (task.deleted).
 */
export function TrashTab({ projectKey, readOnly }: { projectKey: string; readOnly: boolean }) {
  const qc = useQueryClient();
  const users = useUsers();
  const timeZone = useTimeZone();
  const queryKey = ["project-trash", projectKey];

  const trash = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const [tasks, deletions] = await Promise.all([
        fetchAll<TaskListItem>(async (cursor) =>
          (await api.api.v1ProjectsTasksList(projectKey, { include_deleted: true, limit: 200, cursor }, { signal }))
            .data,
        ),
        fetchAll<AuditEntry>(async (cursor) =>
          (await api.api.v1ProjectsAuditList(projectKey, { action: "task.deleted", limit: 200, cursor }, { signal }))
            .data,
        ),
      ]);
      return trashItems(tasks, deletions);
    },
  });

  const restore = useMutation({
    mutationFn: (key: string) => api.api.v1TasksRestoreCreate(key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey });
      // Задача вернулась на доску, в бэклог и «Мои задачи».
      qc.invalidateQueries({ queryKey: boardQueryKey(projectKey) });
      qc.invalidateQueries({ queryKey: myTasksQueryKey });
    },
  });

  if (trash.isPending) return <p className="muted">Загрузка…</p>;
  if (trash.isError) return <p role="alert">{errorText(trash.error)}</p>;
  if (trash.data.length === 0) return <p className="muted">Корзина пуста.</p>;
  return (
    <>
      {restore.isError && <p role="alert">{errorText(restore.error)}</p>}
      <table className="table">
        <thead>
          <tr>
            <th>Ключ</th>
            <th>Название</th>
            <th>Кто удалил</th>
            <th>Когда</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {trash.data.map((t) => (
            <tr key={t.key}>
              <td className="nowrap">
                <Link to={`/tasks/${t.key}`} className="card-key">
                  {t.key}
                </Link>
              </td>
              <td>{t.title}</td>
              <td>{t.deletedBy ? userName(users.data, t.deletedBy) : "—"}</td>
              <td className="nowrap">{formatDateTime(t.deletedAt, timeZone)}</td>
              <td>
                {!readOnly && (
                  <button
                    type="button"
                    disabled={restore.isPending}
                    onClick={() => restore.mutate(t.key)}
                  >
                    Восстановить
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
