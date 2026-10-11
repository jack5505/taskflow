// Переменные стенда E2E; их печатает e2e/stack.sh up.
export const BASE_URL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:8199";

/** Администратор, от имени которого готовятся данные. Пароль задаёт global-setup по ссылке. */
export const ADMIN_EMAIL = "e2e-admin@example.com";
export const ADMIN_PASSWORD = "e2e-admin-password";

/** Ссылки установки пароля из вывода `taskflow admin create` (ТЗ §7.1). */
export const ADMIN_LINK = process.env.E2E_ADMIN_LINK ?? "";
export const LOGIN_EMAIL = process.env.E2E_LOGIN_EMAIL ?? "";
export const LOGIN_LINK = process.env.E2E_LOGIN_LINK ?? "";
