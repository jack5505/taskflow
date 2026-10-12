// Запросы профиля: PATCH /me, смена пароля и письмо, если текущий забыт, «Выйти везде», привязка и отвязка Telegram.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, changePassword, logoutAll } from "../api";
import type { MeProfile, MeUpdate } from "../api/client";
import { meQueryKey } from "../auth/hooks";
import { linkCodeFrom } from "./logic";

/** Изменить профиль: ответ сервера (весь профиль) сразу в кэш — пояс и имя применятся везде. */
export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: MeUpdate) => (await api.api.v1MePartialUpdate(body)).data,
    onSuccess: (me: MeProfile) => qc.setQueryData(meQueryKey, me),
  });
}

/** Смена пароля с вводом текущего (§7.8): остальные сессии сервер отзывает, эта остаётся. */
export function usePasswordChange() {
  return useMutation({
    mutationFn: ({ current, next }: { current: string; next: string }) => changePassword(current, next),
  });
}

/**
 * «Забыли текущий пароль?» — тем же путём, что восстановление (§7.1, §9.1): ссылка на email
 * пользователя, новый пароль задаётся по ней, после установки все сессии отзываются.
 */
export function usePasswordLink() {
  return useMutation({
    mutationFn: (email: string) => api.api.v1AuthPasswordResetCreate({ email }),
  });
}

/** «Выйти везде»: после ответа сессия сброшена, на /login переводит RequireAuth. */
export function useLogoutAll() {
  return useMutation({ mutationFn: logoutAll });
}

/** Выдача кода привязки Telegram (§8.1): POST /me/telegram-link-code. */
export function useLinkCode() {
  return useMutation({
    mutationFn: async () => linkCodeFrom((await api.api.v1MeTelegramLinkCodeCreate()).data),
  });
}

/** «Отвязать» (§8.1, п. 6): DELETE /me/telegram, затем профиль перечитывается. */
export function useUnlinkTelegram() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.api.v1MeTelegramDelete(),
    onSuccess: () => qc.invalidateQueries({ queryKey: meQueryKey }),
  });
}

/** Привязать GitHub (§12.5): ссылка входа в GitHub, браузер уходит на неё. */
export function useGitHubConnect() {
  return useMutation({
    mutationFn: async () => (await api.api.v1MeGithubConnectList()).data,
    onSuccess: (link) => {
      if (!link.url) throw new Error("Сервер не дал ссылку входа в GitHub");
      window.location.assign(link.url);
    },
  });
}

/** Отвязать GitHub: DELETE /me/github, профиль перечитывается. */
export function useGitHubUnlink() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.api.v1MeGithubDelete(),
    onSuccess: () => qc.invalidateQueries({ queryKey: meQueryKey }),
  });
}
