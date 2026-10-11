// Обёртка над fetch для сгенерированного клиента: автообновление access-токена по 401 (ТЗ §9.1).
// Вынесена отдельно от клиента, чтобы логику можно было проверить юнит-тестами без сети.

type Fetch = typeof fetch;

export interface AuthFetchOptions {
  /** Обычный fetch, которым уходит запрос. */
  baseFetch: Fetch;
  /** Текущий access-токен из памяти. */
  getToken: () => string | null;
  /** Обновить сессию: вернуть новый access-токен или null, если сессии больше нет. */
  refresh: () => Promise<string | null>;
  /** Итог обновления — один раз на запрос /auth/refresh: новый токен или null. */
  onRefreshed: (token: string | null) => void;
}

export interface AuthFetch {
  fetch: Fetch;
  /** Обновить токен; одновременные вызовы делят один запрос /auth/refresh. */
  refreshOnce: () => Promise<string | null>;
}

export function createAuthFetch({ baseFetch, getToken, refresh, onRefreshed }: AuthFetchOptions): AuthFetch {
  let inflight: Promise<string | null> | null = null;

  // Refresh-токен одноразовый (сервер отзывает старый при обновлении), поэтому два параллельных
  // /auth/refresh с одной cookie недопустимы: второй получил бы 401 и выбросил пользователя.
  const refreshOnce = () => {
    inflight ??= refresh()
      .catch(() => null)
      .then((t) => {
        onRefreshed(t);
        return t;
      })
      .finally(() => {
        inflight = null;
      });
    return inflight;
  };

  const authFetch: Fetch = async (input, init) => {
    const response = await baseFetch(input, init);
    if (response.status !== 401) return response;

    // Запрос без Authorization (вход, refresh, восстановление пароля) не обновляем:
    // его 401 — ответ по существу, например «Неверный email или пароль».
    const sent = new Headers(init?.headers).get("Authorization");
    if (!sent) return response;

    // Пока запрос летел, токен уже могли обновить другим запросом — тогда просто повторяем.
    const current = getToken();
    const fresh = current && `Bearer ${current}` !== sent ? current : await refreshOnce();
    if (!fresh) return response;

    const headers = new Headers(init?.headers);
    headers.set("Authorization", `Bearer ${fresh}`);
    return baseFetch(input, { ...init, headers });
  };

  return { fetch: authFetch, refreshOnce };
}
