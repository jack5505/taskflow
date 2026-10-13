import { keepPreviousData, useMutation, useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { api } from "../api";
import type { ReportReport } from "../api/client";
import { useMe } from "../auth/hooks";
import { useSprints } from "../backlog/useSprints";
import { formatDuration } from "../lib/duration";
import { formatDate } from "../lib/format";
import { statusLabel } from "../lib/status";
import { useProjectMembers, useProjectsList } from "../projects/hooks";
import {
  csvFileName,
  filterFromParams,
  formatHours,
  paramsFromFilter,
  projectOptions,
  REPORT_VIEWS,
  reportErrorText,
  reportQuery,
  reportQueryKey,
  unwrapBlobError,
  userLocked,
  weekday,
  withPeriod,
  withProject,
  type ReportFilter,
} from "./logic";
import { DateField } from "../ui/DateField";

/** Длительность «2h 30m» и под ней десятичные часы «2.50» (§6.1); ноль — прочерк. */
function Minutes({ value }: { value: number | undefined }) {
  if (!value) return <span className="muted">—</span>;
  return (
    <>
      <span className="nowrap">{formatDuration(value)}</span>
      <span className="report-hours">{formatHours(value)}</span>
    </>
  );
}

/** По пользователям: строка на пользователя, столбец на день, итоги по строке и столбцу (§6.6). */
function ByUser({ r }: { r: ReportReport }) {
  const days = r.days ?? [];
  const users = r.users ?? [];
  if (users.length === 0) return <p className="muted">За период нет списанного времени.</p>;
  return (
    <div className="report-scroll">
      <table className="table report-table">
        <thead>
          <tr>
            <th>Пользователь</th>
            {days.map((d) => (
              <th key={d.date} className={d.weekend ? "weekend" : undefined}>
                {weekday(d.date ?? "")}
                <br />
                {formatDate(d.date)}
              </th>
            ))}
            <th>Итого</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.user_id}>
              <td>{u.user_name}</td>
              {days.map((d, i) => (
                <td key={d.date} className={d.weekend ? "weekend" : undefined}>
                  <Minutes value={u.minutes?.[i]} />
                </td>
              ))}
              <td className="report-total">
                <Minutes value={u.total_minutes} />
              </td>
            </tr>
          ))}
        </tbody>
        <tfoot>
          <tr>
            <th>Итого</th>
            {days.map((d, i) => (
              <td key={d.date} className={d.weekend ? "weekend report-total" : "report-total"}>
                <Minutes value={r.day_totals?.[i]} />
              </td>
            ))}
            <td className="report-total">
              <Minutes value={r.total_minutes} />
            </td>
          </tr>
        </tfoot>
      </table>
    </div>
  );
}

/** По задачам: ключ, название, исполнитель, статус, оценка, списано за период и всего (§6.6). */
function ByTask({ r }: { r: ReportReport }) {
  const tasks = r.tasks ?? [];
  if (tasks.length === 0) return <p className="muted">За период нет списанного времени.</p>;
  return (
    <table className="table report-table report-tasks">
      <thead>
        <tr>
          <th>Ключ</th>
          <th>Название</th>
          <th>Исполнитель</th>
          <th>Статус</th>
          <th>Оценка</th>
          <th>За период</th>
          <th>Всего</th>
        </tr>
      </thead>
      <tbody>
        {tasks.map((t) => (
          <tr key={t.task_id} className={t.deleted ? "deleted" : undefined}>
            <td className="nowrap">
              {/* Удалённая задача не открывается (404), поэтому без ссылки. */}
              {t.deleted ? <span className="card-key">{t.key}</span> : <Link to={`/tasks/${t.key}`}>{t.key}</Link>}
            </td>
            <td>
              {t.title}
              {t.deleted && <span className="badge report-deleted">удалённая задача</span>}
            </td>
            <td>{t.assignee_name || <span className="muted">—</span>}</td>
            <td className="nowrap">{statusLabel(t.status)}</td>
            <td>
              <Minutes value={t.estimate_minutes} />
            </td>
            <td>
              <Minutes value={t.period_minutes} />
            </td>
            <td>
              <Minutes value={t.total_minutes} />
            </td>
          </tr>
        ))}
      </tbody>
      <tfoot>
        <tr>
          <th colSpan={5}>Итого за период</th>
          <td className="report-total">
            <Minutes value={r.total_minutes} />
          </td>
          <td />
        </tr>
      </tfoot>
    </table>
  );
}

/** Сохранить файл из памяти: временная ссылка с download. */
function saveFile(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.append(a);
  a.click();
  a.remove();
  // Firefox и Safari читают файл уже после click(): ссылку освобождаем с запасом.
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** Отчёты по времени (ТЗ §6.6): фильтры в адресе, два представления, выгрузка в CSV. */
export function ReportsPage() {
  const [params, setParams] = useSearchParams();
  const f = filterFromParams(params);
  const me = useMe();
  const admin = me.data?.is_system_admin === true;
  // Администратор системы видит и проекты, где он не участник; удалённые в отчёт не входят.
  const active = useProjectsList("active", admin);
  const archived = useProjectsList("archived", admin);
  const projects = projectOptions(me.data, [...(active.data ?? []), ...(archived.data ?? [])]);
  const locked = userLocked(me.data, projects, f.project);
  const myId = me.data?.id ?? "";

  const sprints = useSprints(f.project);
  const members = useProjectMembers(locked ? "" : f.project);

  const query = reportQuery(f, locked ? myId : null);
  const report = useQuery({
    queryKey: [...reportQueryKey, query],
    queryFn: async ({ signal }) => (await api.api.v1ReportsTimeList(query, { signal })).data,
    enabled: !!me.data,
    // Пока строится новый отчёт, на экране остаётся прежний, а не пустое место.
    placeholderData: keepPreviousData,
  });
  const r = report.data;
  // Прежний отчёт (на экране, пока строится новый) не годится как источник границ периода и итогов.
  const current = report.isPlaceholderData ? undefined : r;

  const csv = useMutation({
    mutationFn: async () => {
      try {
        // Клиент по умолчанию разбирает ответ как JSON; для файла — как Blob. Запрос идёт тем же
        // клиентом, поэтому токен подставляется и обновляется по 401 как у остальных.
        const res = await api.api.v1ReportsTimeList({ ...query, format: "csv" }, { format: "blob" });
        const name = csvFileName(res.headers.get("Content-Disposition"), {
          from: current?.from ?? f.from,
          to: current?.to ?? f.to,
          view: f.view,
        });
        saveFile(res.data as unknown as Blob, name);
      } catch (e) {
        throw await unwrapBlobError(e);
      }
    },
  });

  const update = (next: ReportFilter) => {
    csv.reset();
    setParams(paramsFromFilter(next), { replace: true });
  };

  // Период по умолчанию считает сервер (текущая неделя по поясу пользователя) — в полях его границы.
  const from = f.from || current?.from || "";
  const to = f.to || current?.to || "";

  // Пользователь из адреса, которого нет в списке (без проекта или вне участников), — по имени из отчёта.
  const userOptions: { id: string; name: string }[] = [];
  if (myId) userOptions.push({ id: myId, name: `${me.data?.display_name || me.data?.email || "Я"} (я)` });
  for (const m of members.data ?? []) {
    if (m.user_id && m.user_id !== myId) userOptions.push({ id: m.user_id, name: m.display_name || m.email || "" });
  }
  if (!locked && f.user && !userOptions.some((u) => u.id === f.user)) {
    const name = r?.users?.find((u) => u.user_id === f.user)?.user_name;
    userOptions.push({ id: f.user, name: name || "Выбранный пользователь" });
  }

  let body;
  if (me.isError) body = <p role="alert">{reportErrorText(me.error)}</p>;
  else if (!me.data) body = <p className="muted">Загрузка…</p>;
  else if (report.isError) body = <p role="alert">{reportErrorText(report.error)}</p>;
  // Прежний отчёт другого представления не показываем: у него нет нужных строк.
  else if (!r || r.group_by !== f.view) body = <p className="muted">Загрузка…</p>;
  else body = f.view === "task" ? <ByTask r={r} /> : <ByUser r={r} />;

  return (
    <section className="reports-page">
      <div className="block-head">
        <h2>Отчёты</h2>
        <button
          type="button"
          className="primary"
          onClick={() => csv.mutate()}
          disabled={csv.isPending || !current || report.isError}
        >
          {csv.isPending ? "Готовим файл…" : "Скачать CSV"}
        </button>
      </div>

      <div className="report-filters">
        <label>
          С
          <DateField value={from} max={to || undefined} onChange={(v) => v && update(withPeriod(f, v, to, "from"))} />
        </label>
        <label>
          По
          <DateField value={to} min={from || undefined} onChange={(v) => v && update(withPeriod(f, from, v, "to"))} />
        </label>
        <button type="button" onClick={() => update({ ...f, from: "", to: "" })} disabled={!f.from && !f.to}>
          Текущая неделя
        </button>
        <label>
          Проект
          <select value={f.project} onChange={(e) => update(withProject(f, e.target.value))}>
            <option value="">Все проекты</option>
            {projects.map((p) => (
              <option key={p.key} value={p.key}>
                {p.key} — {p.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Спринт
          <select
            value={f.sprint}
            disabled={!f.project}
            title={f.project ? undefined : "Сначала выберите проект"}
            onChange={(e) => update({ ...f, sprint: e.target.value })}
          >
            <option value="">Все спринты</option>
            {/* Спринт из адреса, которого нет у проекта, — виден, а не молча подменён «Все спринты». */}
            {f.sprint && sprints.data && !sprints.data.some((s) => s.id === f.sprint) && (
              <option value={f.sprint}>Спринт не найден</option>
            )}
            {(sprints.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
                {s.state === "active" ? " (активный)" : s.state === "closed" ? " (закрыт)" : ""}
              </option>
            ))}
          </select>
        </label>
        <label>
          Пользователь
          <select
            value={locked ? myId : f.user}
            disabled={locked}
            title={locked ? "Исполнитель видит отчёты только по себе" : undefined}
            onChange={(e) => update({ ...f, user: e.target.value })}
          >
            {!locked && <option value="">Все пользователи</option>}
            {userOptions.map((u) => (
              <option key={u.id} value={u.id}>
                {u.name}
              </option>
            ))}
          </select>
        </label>
      </div>
      {locked && <p className="muted report-hint">Исполнитель видит отчёты только по себе.</p>}
      {!locked && !f.project && (
        <p className="muted report-hint">Чтобы выбрать другого пользователя, выберите проект.</p>
      )}
      {csv.isError && <p role="alert">Не удалось скачать CSV: {reportErrorText(csv.error)}</p>}

      <nav className="tabs report-tabs" role="tablist" aria-label="Представление отчёта">
        {REPORT_VIEWS.map((v) => (
          <button
            key={v.id}
            type="button"
            role="tab"
            aria-selected={f.view === v.id}
            className={f.view === v.id ? "tab active" : "tab"}
            onClick={() => update({ ...f, view: v.id })}
          >
            {v.label}
          </button>
        ))}
        {current && (
          <span className="muted report-period">
            {formatDate(current.from)} — {formatDate(current.to)} · всего {current.total} ({current.hours} ч)
          </span>
        )}
      </nav>
      <div role="tabpanel" className={report.isFetching && r ? "report-body loading-dim" : "report-body"}>
        {body}
      </div>
    </section>
  );
}
