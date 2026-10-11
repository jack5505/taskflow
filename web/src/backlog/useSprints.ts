import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { SprintView } from "../api/client";
import { sprintsQueryKey } from "./logic";

/** Спринты проекта: активный, запланированные, затем закрытые — новые первыми (порядок сервера). */
export function useSprints(key: string) {
  return useQuery({
    queryKey: sprintsQueryKey(key),
    queryFn: async ({ signal }): Promise<SprintView[]> => (await api.api.v1ProjectsSprintsList(key, { signal })).data ?? [],
    enabled: key !== "",
  });
}
