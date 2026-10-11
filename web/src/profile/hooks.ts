// Запросы профиля: PATCH /me, письмо для смены пароля, «Выйти везде», код привязки Telegram.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, logoutAll } from "../api";
import type { MeProfile, MeUpdate } from "../api/client";
import { meQueryKey } from "../auth/hooks";
import type { TelegramLinkCode } from "./logic";

/** Изменить профиль: ответ сервера (весь профиль) сразу в кэш — пояс и имя применятся везде. */
export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: MeUpdate) => (await api.api.v1MePartialUpdate(body)).data,
    onSuccess: (me: MeProfile) => qc.setQueryData(meQueryKey, me),
  });
}

/**
 * Смена пароля (§7.8): в API нет смены пароля с вводом текущего, поэтому — тем же путём, что и
 * восстановление (§7.1, §9.1): ссылка на email пользователя, новый пароль задаётся по ней,
 * после установки все сессии отзываются.
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

/**
 * Выдача кода привязки Telegram (§8.1). Эндпоинта пока нет — он появится в issue #49
 * (ожидаемый контракт: POST /api/v1/me/telegram/link-code → {code, expires_at, url}).
 * Пока здесь null, кнопка «Привязать Telegram» не показывается (web/CLAUDE.md: кнопку,
 * которую сервер отклонит, не показываем). После #49 и `npm run gen:api` достаточно заменить
 * на `async () => (await api.api.<метод>()).data`.
 */
export const requestLinkCode: (() => Promise<TelegramLinkCode>) | null = null;

export function useLinkCode(request: (() => Promise<TelegramLinkCode>) | null = requestLinkCode) {
  return useMutation({
    mutationFn: async () => {
      if (!request) throw new Error("Привязка Telegram из профиля пока недоступна");
      return request();
    },
  });
}
