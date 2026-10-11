// Сессия пользователя (ТЗ §9.1, web/CLAUDE.md): access-токен живёт только в памяти вкладки,
// refresh-токен — в httpOnly cookie, до которой JavaScript не дотягивается. Это не глобальный
// стор данных: здесь только токен и признак «вошёл / не вошёл», данные сервера — в TanStack Query.
// Держать токен вне React приходится: им пользуется HTTP-клиент, который живёт вне дерева компонентов.

export type AuthStatus = "pending" | "in" | "out";

let token: string | null = null;
let status: AuthStatus = "pending";
// Номер сессии растёт при выходе: ответы, отправленные от прежней сессии, по нему отбрасываются.
let epoch = 0;
// Почему сессии нет: выход по кнопке — или истекла/не восстановилась (тогда после входа
// вернём пользователя на страницу, где он был).
let outReason: "logout" | "expired" = "expired";
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

export function getToken(): string | null {
  return token;
}

export function getStatus(): AuthStatus {
  return status;
}

export function getEpoch(): number {
  return epoch;
}

export function signIn(accessToken: string) {
  token = accessToken;
  status = "in";
  emit();
}

export function getSignOutReason() {
  return outReason;
}

export function signOut(reason: "logout" | "expired" = "expired") {
  outReason = reason;
  if (status === "out" && token === null) return;
  epoch++;
  token = null;
  status = "out";
  emit();
}

export function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
