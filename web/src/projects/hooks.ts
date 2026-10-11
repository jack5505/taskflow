// Запросы проекта, общие для доски и карточки задачи: один кэш TanStack Query на оба экрана.
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { MeProfile, ProjectListItem, ProjectMemberView, ProjectView } from "../api/client";
import type { Actor } from "../board/transitions";
import { listQuery, projectsQueryKey } from "./logic";

export const projectQueryKey = (key: string) => ["project", key] as const;
export const membersQueryKey = (key: string) => ["project-members", key] as const;

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

/**
 * Активный спринт проекта — из карточки проекта в общем списке (тот же кэш, что на главной):
 * отдельного эндпоинта спринтов пока нет.
 */
export function useActiveSprint(key: string, project: ProjectView | undefined) {
  const listFilter = project?.is_archived ? "archived" : "active";
  const projects = useQuery({
    queryKey: [...projectsQueryKey, listFilter],
    queryFn: async ({ signal }): Promise<ProjectListItem[]> =>
      (await api.api.v1ProjectsList(listQuery(listFilter), { signal })).data ?? [],
    enabled: !!project,
  });
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
