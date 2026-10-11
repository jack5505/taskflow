// Обёртка над fetch для сгенерированного клиента: автообновление access-токена по 401 (ТЗ §9.1).
// Вынесена отдельно от клиента, чтобы логику можно было проверить юнит-тестами без сети.

type Fetch = typeof fetch;

export interface AuthFetchOptions {
  /** Обычный fetch, которым уходит запрос. */
  baseFetch: Fetch;
  /** Текущий access-токен из памяти. */
  getToken: () => string | null;
  /** Номер сессии: меняется при выходе. Ответ, пришедший после смены сессии, не повторяется. */
  getEpoch: () => number;
  /**
   * Обновить сессию: новый access-токен или null, если сессии больше нет (сервер ответил 401/403).
   * Временный сбой (сеть, 5xx) — исключение: сессия, возможно, жива, выходить рано.
   */
  refresh: () => Promise<string | null>;
  /** Окончательный итог обновления — один раз на запрос /auth/refresh: новый токен или null. */
  onRefreshed: (token: string | null) => void;
}

export interface AuthFetch {
  fetch: Fetch;
  /** Обновить токен; одновременные вызовы делят один запрос /auth/refresh. Сбой — исключение. */
  refreshOnce: () => Promise<string | null>;
  /** Дождаться обновления, если оно идёт (перед выходом, чтобы оно не вернуло сессию). */
  idle: () => Promise<void>;
}

export function createAuthFetch(opts: AuthFetchOptions): AuthFetch {
  const { baseFetch, getToken, getEpoch, refresh, onRefreshed } = opts;
  let inflight: Promise<string | null> | null = null;

  // Refresh-токен одноразовый: сервер отзывает старый при обновлении, а повторное предъявление
  // отозванного считает кражей и закрывает все сессии. Поэтому параллельные 401 делят один refresh.
  const refreshOnce = () => {
    inflight ??= (async () => {
      const epoch = getEpoch();
      const t = await refresh();
      // Пока шёл refresh, пользователь вышел — его результат уже никому не нужен.
      if (getEpoch() !== epoch) return null;
      onRefreshed(t);
      return t;
    })().finally(() => {
      inflight = null;
    });
    return inflight;
  };

  const authFetch: Fetch = async (input, init) => {
    const epoch = getEpoch();
    const response = await baseFetch(input, init);
    if (response.status !== 401) return response;

    // Запрос без Authorization (вход, refresh, восстановление пароля) не обновляем:
    // его 401 — ответ по существу, например «Неверный email или пароль».
    const sent = new Headers(init?.headers).get("Authorization");
    if (!sent) return response;
    // Запрос ушёл от прежней сессии (выход, вход другим пользователем) — не повторяем от новой.
    if (getEpoch() !== epoch) return response;

    // Пока запрос летел, токен уже могли обновить другим запросом — тогда просто повторяем.
    const current = getToken();
    let fresh: string | null;
    if (current && `Bearer ${current}` !== sent) {
      fresh = current;
    } else {
      try {
        fresh = await refreshOnce();
      } catch {
        return response; // временный сбой: отдаём 401 запросу, сессию не трогаем
      }
    }
    if (!fresh) return response;

    const headers = new Headers(init?.headers);
    headers.set("Authorization", `Bearer ${fresh}`);
    return baseFetch(input, { ...init, headers });
  };

  const idle = async () => {
    await inflight?.catch(() => null);
  };

  return { fetch: authFetch, refreshOnce, idle };
}
