import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api } from "../api";
import type { TaskMyTask } from "../api/client";
import { errorText } from "../api/errors";
import { formatSpentEstimate } from "../lib/duration";
import { formatDate } from "../lib/format";
import { statusLabel } from "../lib/status";
import { myTasksQueryKey } from "./logic";

/**
 * Блок «Мои задачи» (ТЗ §7.2): открытые задачи пользователя по всем проектам. Порядок задаёт
 * сервер — по сроку, без срока в конце, поэтому просроченные оказываются сверху.
 */
export function MyTasks() {
  const tasks = useQuery({
    queryKey: myTasksQueryKey,
    queryFn: async ({ signal }): Promise<TaskMyTask[]> => (await api.api.v1MeTasksList({ signal })).data ?? [],
  });

  return (
    <section className="block">
      <h2>Мои задачи</h2>
      {tasks.isPending && <p className="muted">Загрузка…</p>}
      {tasks.isError && <p role="alert">{errorText(tasks.error)}</p>}
      {tasks.data && tasks.data.length === 0 && <p className="muted">Открытых задач нет.</p>}
      {tasks.data && tasks.data.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>Ключ</th>
              <th>Название</th>
              <th>Проект</th>
              <th>Статус</th>
              <th>Срок</th>
              <th>Списано / оценка</th>
            </tr>
          </thead>
          <tbody>
            {tasks.data.map((t) => (
              <tr key={t.key}>
                <td className="nowrap">{t.key}</td>
                <td>
                  <Link to={`/tasks/${t.key}`}>{t.title}</Link>
                </td>
                <td>
                  <Link to={`/projects/${t.project_key}`}>{t.project_name}</Link>
                </td>
                <td className="nowrap">{statusLabel(t.status)}</td>
                <td className={t.overdue ? "nowrap overdue" : "nowrap"} title={t.overdue ? "Просрочена" : undefined}>
                  {formatDate(t.due_date) || "—"}
                </td>
                <td className="nowrap">{formatSpentEstimate(t.spent_minutes, t.estimate_minutes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
