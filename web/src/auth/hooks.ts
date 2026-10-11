import { useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api";
import type { MeProfile } from "../api/client";
import { getStatus, subscribe, type AuthStatus } from "./session";

export function useAuthStatus(): AuthStatus {
  return useSyncExternalStore(subscribe, getStatus);
}

export const meQueryKey = ["me"] as const;

/** Профиль текущего пользователя (GET /me): имя для шапки, часовой пояс для дат. */
export function useMe() {
  const status = useAuthStatus();
  return useQuery({
    queryKey: meQueryKey,
    queryFn: async ({ signal }): Promise<MeProfile> => (await api.api.v1MeList({ signal })).data,
    enabled: status === "in",
    staleTime: 5 * 60_000,
  });
}

/** Часовой пояс для показа времени (ТЗ §7): из профиля, пока профиль грузится — пояс браузера. */
export function useTimeZone(): string | undefined {
  return useMe().data?.timezone || undefined;
}
