// Единственная точка доступа к API из приложения (web/CLAUDE.md: только сгенерированный клиент,
// без ручных fetch). client.ts генерируется командой `npm run gen:api` из api/internal/docs/swagger.json —
// править его руками нельзя.
import { getToken, signIn, signOut } from "../auth/session";
import { createAuthFetch } from "./authFetch";
import { Api } from "./client";

// /auth/refresh и /auth/logout без этого заголовка сервер отклоняет с 403 (защита от CSRF).
export const csrfHeaders = { "X-Requested-With": "XMLHttpRequest" };

const auth = createAuthFetch({
  baseFetch: (...args) => fetch(...args),
  getToken,
  refresh: async () => {
    const r = await api.api.v1AuthRefreshCreate({ headers: csrfHeaders });
    return r.data.access_token ?? null;
  },
  onRefreshed: (t) => (t ? signIn(t) : signOut()),
});

export const api = new Api<null>({
  // Тот же origin: фронтенд встроен в бинарник api; в dev Vite проксирует /api на 8100.
  baseUrl: "",
  securityWorker: () => {
    const t = getToken();
    return t ? { headers: { Authorization: `Bearer ${t}` } } : {};
  },
  customFetch: auth.fetch,
});

/**
 * Обновить сессию по cookie: при старте приложения (восстановление после перезагрузки) и
 * при 401 на защищённом запросе. Неудача означает, что сессии нет — пользователь идёт на вход.
 */
export async function refreshSession(): Promise<boolean> {
  return (await auth.refreshOnce()) !== null;
}

export type { HttpResponse, RequestParams } from "./client";
