// Разбор ошибок API в едином формате ТЗ §9.3: {"error":{"code","message","details"}}.
// Сгенерированный клиент на ответ не-2xx бросает сам ответ с полем error; сбой сети — TypeError.

export interface ApiError {
  status: number;
  code: string;
  message: string;
  details: Record<string, unknown>;
}

export interface FieldError {
  field: string;
  reason: string;
}

export function parseApiError(e: unknown): ApiError | null {
  if (!(e instanceof Response)) return null;
  const body = (e as Response & { error?: unknown }).error;
  const err =
    body && typeof body === "object" && "error" in body
      ? ((body as { error?: Partial<ApiError> }).error ?? {})
      : {};
  return {
    status: e.status,
    code: err.code ?? "",
    message: err.message ?? "",
    details: err.details ?? {},
  };
}

export function fieldErrors(e: ApiError | null): FieldError[] {
  const fields = e?.details.fields;
  return Array.isArray(fields) ? (fields as FieldError[]) : [];
}

/** Текст для пользователя: сообщение сервера, иначе общий текст по ситуации. */
export function errorText(e: unknown, fallback = "Не удалось выполнить запрос, попробуйте ещё раз"): string {
  const err = parseApiError(e);
  if (!err) return "Нет связи с сервером, проверьте подключение";
  return err.message || fallback;
}
