// Запросы проекта, общие для доски и карточки задачи: один кэш TanStack Query на оба экрана.
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { MeProfile, ProjectListItem, ProjectMemberView, ProjectView, TaskListItem } from "../api/client";
import type { Actor } from "../board/transitions";
import { listQuery, projectsQueryKey, type ProjectsFilter } from "./logic";

export const projectQueryKey = (key: string) => ["project", key] as const;
export const membersQueryKey = (key: string) => ["project-members", key] as const;

/** Параметры GET /projects/{key}/tasks — как в сгенерированном клиенте. */
export type TasksListQuery = NonNullable<Parameters<typeof api.api.v1ProjectsTasksList>[1]>;

/** Все задачи по запросу: сервер отдаёт до 200 за раз, остальное — по next_cursor. */
export async function fetchAllTasks(key: string, query: TasksListQuery, signal: AbortSignal): Promise<TaskListItem[]> {
  const items: TaskListItem[] = [];
  let cursor: string | undefined;
  do {
    const page = (await api.api.v1ProjectsTasksList(key, { ...query, cursor }, { signal })).data;
    items.push(...(page.items ?? []));
    cursor = page.next_cursor || undefined;
  } while (cursor);
  return items;
}

export function useProject(key: string) {
  return useQuery({
    queryKey: projectQueryKey(key),
    queryFn: async ({ signal }) => (await api.api.v1ProjectsDetail(key, { signal })).data,
    enabled: key !== "",
  });
}

export function useProjectMembers(key: string) {
  return useQuery({
    queryKey: membersQueryKey(key),
    queryFn: async ({ signal }): Promise<ProjectMemberView[]> =>
      (await api.api.v1ProjectsMembersList(key, { signal })).data ?? [],
    enabled: key !== "",
  });
}

/** Список проектов (§7.2) — один кэш на главную, доску, карточку и окно создания задачи. */
export function useProjectsList(filter: ProjectsFilter, enabled = true) {
  return useQuery({
    queryKey: [...projectsQueryKey, filter],
    queryFn: async ({ signal }): Promise<ProjectListItem[]> =>
      (await api.api.v1ProjectsList(listQuery(filter), { signal })).data ?? [],
    enabled,
  });
}

/**
 * Активный спринт проекта — из карточки проекта в общем списке (тот же кэш, что на главной):
 * отдельного эндпоинта спринтов пока нет.
 */
export function useActiveSprint(key: string, project: ProjectView | undefined) {
  const projects = useProjectsList(project?.is_archived ? "archived" : "active", !!project);
  return { query: projects, sprint: projects.data?.find((p) => p.key === key)?.active_sprint };
}

/** Кто действует в проекте; администратор системы — с правами менеджера (§2). */
export function actorFor(me: MeProfile | undefined, project: ProjectView | undefined): Actor | null {
  if (!me || !project) return null;
  return {
    userId: me.id ?? "",
    manager: project.my_role === "manager" || me.is_system_admin === true,
    member: !!project.my_role,
  };
}
