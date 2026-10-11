// Правила нового пароля (ТЗ §7.1) — для установки по ссылке и смены в профиле. Проверка до
// запроса — подсказка, правило проверяет сервер (api/internal/auth: validatePassword).

/** Не короче 10 символов. */
export const MIN_PASSWORD = 10;
/** Предел bcrypt — 72 байта UTF-8. */
const MAX_PASSWORD_BYTES = 72;

/** Ошибка нового пароля или пустая строка. Длина — в символах, а не в единицах UTF-16. */
export function newPasswordError(password: string): string {
  if ([...password].length < MIN_PASSWORD) return `Пароль должен быть не короче ${MIN_PASSWORD} символов`;
  if (new TextEncoder().encode(password).length > MAX_PASSWORD_BYTES)
    return "Пароль слишком длинный: не больше 72 байт (около 36 букв кириллицы)";
  return "";
}
