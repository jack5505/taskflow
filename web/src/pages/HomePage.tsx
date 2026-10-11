import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router";
import { api } from "../api";
import type { ProjectListItem } from "../api/client";
import { errorText } from "../api/errors";
import { useMe } from "../auth/hooks";
import { CreateProjectForm } from "../projects/CreateProjectForm";
import {
  filterFromParams,
  isSystemAdmin,
  listQuery,
  paramsForFilter,
  projectsQueryKey,
  visibleFilters,
} from "../projects/logic";
import { MyTasks } from "../projects/MyTasks";
import { ProjectCard } from "../projects/ProjectCard";

const emptyText = {
  active: "Проектов пока нет.",
  archived: "Архивных проектов нет.",
  deleted: "Удалённых проектов нет.",
};

// Главная — список проектов с блоком «Мои задачи» (ТЗ §7.2). Фильтр живёт в адресе.
export function HomePage() {
  const me = useMe();
  const admin = isSystemAdmin(me.data);
  const [params, setParams] = useSearchParams();
  const filter = filterFromParams(params, admin);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);

  const projects = useQuery({
    queryKey: [...projectsQueryKey, filter],
    queryFn: async ({ signal }): Promise<ProjectListItem[]> =>
      (await api.api.v1ProjectsList(listQuery(filter), { signal })).data ?? [],
    // «Удалённые» не администратору сервер отклонит: ждём профиль, чтобы не слать лишний запрос.
    enabled: me.isSuccess,
  });

  const restore = useMutation({
    mutationFn: (key: string) => api.api.v1ProjectsRestoreCreate(key),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: projectsQueryKey }),
  });

  return (
    <>
      <MyTasks />
      <section className="block">
        <div className="block-head">
          <h2>Проекты</h2>
          <nav className="tabs" aria-label="Фильтр проектов">
            {visibleFilters(admin).map((f) => (
              <button
                key={f.value}
                type="button"
                className={f.value === filter ? "tab active" : "tab"}
                aria-pressed={f.value === filter}
                onClick={() => setParams(paramsForFilter(f.value))}
              >
                {f.label}
              </button>
            ))}
          </nav>
          {admin && !creating && (
            <button type="button" className="primary" onClick={() => setCreating(true)}>
              Создать проект
            </button>
          )}
        </div>
        {admin && creating && (
          <CreateProjectForm
            onCancel={() => setCreating(false)}
            onCreated={() => {
              setCreating(false);
              void queryClient.invalidateQueries({ queryKey: projectsQueryKey });
            }}
          />
        )}
        {restore.isError && <p role="alert">{errorText(restore.error)}</p>}
        {(me.isPending || projects.isPending) && !me.isError && <p className="muted">Загрузка…</p>}
        {me.isError && <p role="alert">{errorText(me.error)}</p>}
        {projects.isError && <p role="alert">{errorText(projects.error)}</p>}
        {projects.data && projects.data.length === 0 && <p className="muted">{emptyText[filter]}</p>}
        {projects.data && projects.data.length > 0 && (
          <div className="cards">
            {projects.data.map((p) => (
              <ProjectCard
                key={p.id}
                project={p}
                onOpen={filter === "deleted" ? undefined : () => navigate(`/projects/${p.key}`)}
                onRestore={filter === "deleted" ? () => restore.mutate(p.key!) : undefined}
                restoring={restore.isPending && restore.variables === p.key}
              />
            ))}
          </div>
        )}
      </section>
    </>
  );
}
