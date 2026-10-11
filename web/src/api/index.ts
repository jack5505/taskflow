// Единственная точка доступа к API из приложения (web/CLAUDE.md: только сгенерированный клиент,
// без ручных fetch). client.ts генерируется командой `npm run gen:api` из api/internal/docs/swagger.json —
// править его руками нельзя.
import { getEpoch, getToken, signIn, signOut } from "../auth/session";
import { createAuthFetch } from "./authFetch";
import { Api } from "./client";
import { parseApiError } from "./errors";

// /auth/refresh и /auth/logout без этого заголовка сервер отклоняет с 403 (защита от CSRF).
export const csrfHeaders = { "X-Requested-With": "XMLHttpRequest" };

// Cookie с refresh-токеном одна на все вкладки. Без межвкладочной блокировки две вкладки,
// обновляющиеся одновременно, предъявят одну cookie дважды — сервер сочтёт это кражей токена
// и закроет все сессии. Под блокировкой вторая вкладка дождётся первой и пойдёт уже с новой cookie.
function withRefreshLock<T>(fn: () => Promise<T>): Promise<T> {
  return "locks" in navigator && navigator.locks
    ? navigator.locks.request("taskflow-auth-refresh", fn)
    : fn();
}

const auth = createAuthFetch({
  baseFetch: (...args) => fetch(...args),
  getToken,
  getEpoch,
  refresh: () =>
    withRefreshLock(async () => {
      try {
        const r = await api.api.v1AuthRefreshCreate({ headers: csrfHeaders });
        return r.data?.access_token ?? null;
      } catch (e) {
        const status = parseApiError(e)?.status;
        if (status === 401 || status === 403) return null; // сессии нет
        throw e; // сеть или 5xx: сессия, возможно, жива
      }
    }),
  onRefreshed: (t) => (t ? signIn(t) : signOut()),
});

export const api = new Api<null>({
  // Тот же origin: фронтенд встроен в бинарник api; в dev Vite проксирует /api на 8100.
  baseUrl: "",
  baseApiParams: {
    credentials: "same-origin",
    headers: {},
    redirect: "follow",
    referrerPolicy: "no-referrer",
    // Разбирать JSON всегда, а не только у эндпоинтов с телом ответа: иначе у ответов 204
    // (password-set, password-reset, logout) клиент не читает тело ошибки §9.3 и details.fields
    // теряются. У успешного 204 разбор пустого тела падает молча — data остаётся null.
    format: "json",
  },
  securityWorker: () => {
    const t = getToken();
    return t ? { headers: { Authorization: `Bearer ${t}` } } : {};
  },
  customFetch: auth.fetch,
});

/**
 * Восстановить сессию по cookie при старте приложения (access-токен в памяти не переживает
 * перезагрузку). Нет сессии или нет связи — на страницу входа: там будет видно, в чём дело.
 */
export async function refreshSession(): Promise<boolean> {
  try {
    return (await auth.refreshOnce()) !== null;
  } catch {
    signOut();
    return false;
  }
}

/** Выйти: дождаться идущего обновления токена, иначе оно вернуло бы сессию после выхода. */
export async function logout(): Promise<void> {
  await auth.idle();
  try {
    await api.api.v1AuthLogoutCreate({ headers: csrfHeaders });
  } finally {
    // Сессию в памяти сбрасываем и при ошибке: пользователь попросил выйти.
    signOut("logout");
  }
}

export type { HttpResponse, RequestParams } from "./client";
