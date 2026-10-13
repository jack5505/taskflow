import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { api } from "../api";
import { errorText } from "../api/errors";
import { useTimeZone } from "../auth/hooks";
import { formatDateTime } from "../lib/format";
import { useUsers, userName } from "./hooks";
import { AUDIT_ACTIONS, auditActionLabel, auditChanges, auditEntity, auditQuery, auditSourceLabel } from "./logic";
import { INVALID_DATE } from "../lib/date";
import { DateField } from "../ui/DateField";

const emptyFilter = { from: "", to: "", userId: "", action: "" };

/**
 * Журнал (§7.9, только администратор системы): действия менеджеров и администраторов по проекту
 * (§3.8) — кто, когда, что изменил; фильтры по периоду, пользователю и типу действия.
 */
export function AuditTab({ projectKey }: { projectKey: string }) {
  const [filter, setFilter] = useState(emptyFilter);
  const users = useUsers();
  const timeZone = useTimeZone();
  const query = auditQuery(filter);

  const audit = useInfiniteQuery({
    queryKey: ["project-audit", projectKey, query],
    queryFn: async ({ pageParam, signal }) =>
      (await api.api.v1ProjectsAuditList(projectKey, { ...query, cursor: pageParam || undefined }, { signal })).data,
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const rows = audit.data?.pages.flatMap((p) => p.items ?? []) ?? [];
  const set = (k: keyof typeof emptyFilter) => (v: string) => setFilter({ ...filter, [k]: v });

  return (
    <>
      <div className="board-filters">
        <label>
          С <DateField revertOnBlur value={filter.from} onChange={(v) => v !== INVALID_DATE && set("from")(v)} />
        </label>
        <label>
          по <DateField revertOnBlur value={filter.to} onChange={(v) => v !== INVALID_DATE && set("to")(v)} />
        </label>
        <select aria-label="Пользователь" value={filter.userId} onChange={(e) => set("userId")(e.target.value)}>
          <option value="">Все пользователи</option>
          {(users.data ?? []).map((u) => (
            <option key={u.id} value={u.id}>
              {u.display_name || u.email}
            </option>
          ))}
        </select>
        <select aria-label="Тип действия" value={filter.action} onChange={(e) => set("action")(e.target.value)}>
          <option value="">Все действия</option>
          {AUDIT_ACTIONS.map((a) => (
            <option key={a.value} value={a.value}>
              {a.label}
            </option>
          ))}
        </select>
        {Object.values(filter).some(Boolean) && (
          <button type="button" onClick={() => setFilter(emptyFilter)}>
            Сбросить
          </button>
        )}
      </div>

      {audit.isError && <p role="alert">{errorText(audit.error)}</p>}
      {audit.isPending && <p className="muted">Загрузка…</p>}
      {audit.isSuccess && rows.length === 0 && <p className="muted">Записей нет.</p>}
      {rows.length > 0 && (
        <table className="table history">
          <thead>
            <tr>
              <th>Когда</th>
              <th>Кто</th>
              <th>Действие</th>
              <th>Над чем</th>
              <th>Изменения</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <tr key={e.id}>
                <td className="nowrap">{formatDateTime(e.created_at, timeZone)}</td>
                <td>
                  {userName(users.data, e.actor_id)}
                  {e.source && <span className="badge">{auditSourceLabel(e.source)}</span>}
                </td>
                <td>{auditActionLabel(e.action)}</td>
                <td>{auditEntity(e, (id) => userName(users.data, id))}</td>
                <td>
                  {auditChanges(e).map((line) => (
                    <div key={line}>{line}</div>
                  ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {audit.hasNextPage && (
        <button type="button" disabled={audit.isFetchingNextPage} onClick={() => audit.fetchNextPage()}>
          {audit.isFetchingNextPage ? "Загрузка…" : "Показать ещё"}
        </button>
      )}
    </>
  );
}
