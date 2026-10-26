import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useParams, useSearchParams } from "react-router";
import type { ProjectMemberView, SprintView } from "../api/client";
import { errorText } from "../api/errors";
import { useTimeZone } from "../auth/hooks";
import { PAGE_LIMIT } from "../board/filters";
import { formatDuration } from "../lib/duration";
import { formatDate } from "../lib/format";
import { fetchAllTasks, useProject, useProjectMembers } from "../projects/hooks";
import { RowCells } from "./BacklogRow";
import { archiveSummary } from "./logic";
import { useSprints } from "./useSprints";
import { ProjectHeader } from "../projects/ProjectHeader";
import { LoadError } from "../ui/LoadError";

/**
 * Сводка закрытого спринта: что в нём осталось после завершения (§5.4 — готовые и отменённые;
 * незакрытые уехали). Отчёт с берндауном (§5.6) — GET /sprints/{id}/report, issue #30.
 */
function ClosedSprint({ projectKey, sprint, members }: { projectKey: string; sprint: SprintView; members: Map<string | undefined, ProjectMemberView> }) {
  const tz = useTimeZone();
  const tasks = useQuery({
    // Не под ключом доски: состав закрытого спринта не меняется, правки задач его не трогают.
    queryKey: ["sprint-archive", projectKey, sprint.id],
    queryFn: ({ signal }) => fetchAllTasks(projectKey, { sprint_id: [sprint.id!], limit: PAGE_LIMIT }, signal),
  });
  const s = archiveSummary(tasks.data ?? []);
  return (
    <section className="panel archive-sprint" aria-label={`Спринт ${sprint.name}`}>
      <h3>{sprint.name}</h3>
      {sprint.goal && <p>Цель: {sprint.goal}</p>}
      <dl className="card-stats">
        <div>
          <dt>Даты</dt>
          <dd>
            {formatDate(sprint.start_date)} — {formatDate(sprint.end_date)}
          </dd>
        </div>
        <div>
          <dt>Завершён</dt>
          <dd>{formatDate(sprint.closed_at, tz)}</dd>
        </div>
        {tasks.isSuccess && (
          <>
            <div>
              <dt>Готово</dt>
              <dd>
                {s.done} {s.cancelled > 0 && <span className="muted">(отменено: {s.cancelled})</span>}
              </dd>
            </div>
            <div>
              <dt>Оценка готовых</dt>
              <dd>{formatDuration(s.doneEstimate)}</dd>
            </div>
            <div>
              <dt>Списано</dt>
              <dd>{formatDuration(s.spent)}</dd>
            </div>
          </>
        )}
      </dl>
      {tasks.error && <p role="alert">{errorText(tasks.error)}</p>}
      {tasks.isPending && <p className="muted">Загрузка задач…</p>}
      {tasks.isSuccess &&
        (tasks.data.length ? (
          <ul className="backlog-rows">
            {tasks.data.map((t) => (
              <li key={t.id} className="backlog-row">
                <RowCells task={t} assignee={members.get(t.assignee_id)} linked timeZone={tz} />
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">Задач не осталось.</p>
        ))}
    </section>
  );
}

/** «Архив спринтов» (§7.4): закрытые спринты, новые первыми; выбранный — в адресе (?sprint=id). */
export function SprintArchivePage() {
  const { key = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const tz = useTimeZone();
  const project = useProject(key);
  const members = useProjectMembers(key);
  const sprints = useSprints(key);
  const closed = useMemo(() => (sprints.data ?? []).filter((s) => s.state === "closed"), [sprints.data]);
  const selected = closed.find((s) => s.id === params.get("sprint"));
  const memberById = useMemo(() => new Map((members.data ?? []).map((m) => [m.user_id, m])), [members.data]);
  const loadError = project.error ?? sprints.error;

  return (
    <section className="backlog-page">
      <ProjectHeader projectKey={key}>
        <span className="muted">Архив спринтов</span>
      </ProjectHeader>
      {loadError && <LoadError error={loadError} onRetry={() => [project, sprints].forEach((q) => q.isError && void q.refetch())} />}
      {sprints.isPending && !loadError && <p className="muted">Загрузка…</p>}
      {sprints.isSuccess && closed.length === 0 && <p className="muted">Завершённых спринтов пока нет.</p>}
      {closed.length > 0 && (
        <div className="table-scroll">
          <table className="table archive-table">
            <thead>
              <tr>
                <th>Спринт</th>
                <th>Даты</th>
                <th>Завершён</th>
                <th>Задач</th>
                <th>Оценка</th>
              </tr>
            </thead>
            <tbody>
              {closed.map((s) => (
                <tr key={s.id} className={s.id === selected?.id ? "selected" : undefined}>
                  <td>
                    <button type="button" className="link" onClick={() => setParams({ sprint: s.id! })}>
                      {s.name}
                    </button>
                  </td>
                  <td className="nowrap">
                    {formatDate(s.start_date)} — {formatDate(s.end_date)}
                  </td>
                  <td className="nowrap">{formatDate(s.closed_at, tz)}</td>
                  <td>{s.tasks_count ?? 0}</td>
                  <td className="nowrap">{formatDuration(s.estimate_minutes ?? 0)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {selected && <ClosedSprint key={selected.id} projectKey={key} sprint={selected} members={memberById} />}
    </section>
  );
}
