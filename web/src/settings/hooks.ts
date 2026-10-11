// Запросы настроек проекта: изменение проекта (PATCH /projects/{key}), список пользователей,
// подключение и отключение Telegram-группы.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ProjectUpdateRequest, ProjectView, UserUser } from "../api/client";
import { projectQueryKey } from "../projects/hooks";
import { projectsQueryKey } from "../projects/logic";
import { bindCodeFrom } from "./logic";

export const usersQueryKey = ["users"] as const;

/**
 * Пользователи системы (GET /users): администратору — все целиком, менеджеру — только активные
 * и кратко (id, email, имя) — для добавления в проект.
 */
export function useUsers(enabled = true) {
  return useQuery({
    queryKey: usersQueryKey,
    queryFn: async ({ signal }): Promise<UserUser[]> => (await api.api.v1UsersList({ signal })).data ?? [],
    enabled,
    staleTime: 60_000,
  });
}

/** Имя пользователя по id для журнала и корзины; неизвестный — сам id. */
export function userName(users: UserUser[] | undefined, id: string | undefined): string {
  if (!id) return "система";
  const u = users?.find((x) => x.id === id);
  return u?.display_name || u?.email || id;
}

/** Изменить проект: ответ сервера кладём в кэш, список проектов на главной перечитываем. */
export function useUpdateProject(key: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: ProjectUpdateRequest) => (await api.api.v1ProjectsPartialUpdate(key, body)).data,
    onSuccess: (p: ProjectView) => {
      qc.setQueryData(projectQueryKey(key), p);
      qc.invalidateQueries({ queryKey: projectsQueryKey });
    },
  });
}

/** Код подключения группы (§8.2): POST /projects/{key}/telegram-bind-code. */
export function useBindCode(key: string) {
  return useMutation({
    mutationFn: async () => bindCodeFrom((await api.api.v1ProjectsTelegramBindCodeCreate(key)).data),
  });
}

/** «Отключить» группу (§8.2, п. 5): DELETE /projects/{key}/telegram, затем проект перечитывается. */
export function useDisconnectGroup(key: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.api.v1ProjectsTelegramDelete(key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectQueryKey(key) });
      qc.invalidateQueries({ queryKey: projectsQueryKey });
    },
  });
}
