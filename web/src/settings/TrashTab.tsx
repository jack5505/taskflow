import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api } from "../api";
import { errorText } from "../api/errors";
import { useTimeZone } from "../auth/hooks";
import { boardQueryKey } from "../board/cards";
import { formatDateTime } from "../lib/format";
import { myTasksQueryKey } from "../projects/logic";
import { historyQueryKey, taskQueryKey } from "../task/keys";
import { trashDeleter } from "./logic";

/**
 * Корзина (§7.9, только администратор системы): удалённые задачи проекта — ключ, название, кто
 * и когда удалил, «Восстановить». Список — GET /projects/{key}/trash: новые удаления сверху,
 * следующая страница — по next_cursor.
 */
export function TrashTab({ projectKey, readOnly }: { projectKey: string; readOnly: boolean }) {
  const qc = useQueryClient();
  const timeZone = useTimeZone();
  const queryKey = ["project-trash", projectKey];

  const trash = useInfiniteQuery({
    queryKey,
    queryFn: async ({ pageParam, signal }) =>
      (await api.api.v1ProjectsTrashList(projectKey, { cursor: pageParam || undefined }, { signal })).data,
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const rows = trash.data?.pages.flatMap((p) => p.items ?? []) ?? [];

  const restore = useMutation({
    mutationFn: (key: string) => api.api.v1TasksRestoreCreate(key),
    onSuccess: (_, key) => {
      qc.invalidateQueries({ queryKey });
      qc.invalidateQueries({ queryKey: taskQueryKey(key) });
      qc.invalidateQueries({ queryKey: historyQueryKey(key) });
      // Задача вернулась на доску, в бэклог и «Мои задачи».
      qc.invalidateQueries({ queryKey: boardQueryKey(projectKey) });
      qc.invalidateQueries({ queryKey: myTasksQueryKey });
    },
  });

  if (trash.isPending) return <p className="muted">Загрузка…</p>;
  if (trash.isError) return <p role="alert">{errorText(trash.error)}</p>;
  if (rows.length === 0) return <p className="muted">Корзина пуста.</p>;
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
          {rows.map((t) => (
            <tr key={t.key}>
              <td className="nowrap">
                <Link to={`/tasks/${t.key}`} className="card-key">
                  {t.key}
                </Link>
              </td>
              <td>{t.title}</td>
              <td>{trashDeleter(t)}</td>
              <td className="nowrap">{formatDateTime(t.deleted_at, timeZone)}</td>
              <td>
                {!readOnly && (
                  <button
                    type="button"
                    disabled={restore.isPending}
                    onClick={() => restore.mutate(t.key ?? "")}
                  >
                    Восстановить
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {trash.hasNextPage && (
        <button type="button" disabled={trash.isFetchingNextPage} onClick={() => trash.fetchNextPage()}>
          {trash.isFetchingNextPage ? "Загрузка…" : "Показать ещё"}
        </button>
      )}
    </>
  );
}
