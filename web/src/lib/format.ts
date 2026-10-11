// Форматирование дат и времени для интерфейса (ТЗ §7): даты dd.mm.yyyy, время — в часовом поясе
// пользователя из профиля (GET /me, поле timezone). Без пояса — пояс браузера.

const DATE_ONLY = /^(\d{4})-(\d{2})-(\d{2})$/;

const formatters = new Map<string, Intl.DateTimeFormat>();

function formatter(timeZone: string | undefined): Intl.DateTimeFormat {
  const key = timeZone ?? "";
  let f = formatters.get(key);
  if (!f) {
    const opts: Intl.DateTimeFormatOptions = {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    };
    try {
      f = new Intl.DateTimeFormat("ru-RU", { ...opts, timeZone });
    } catch {
      // Неизвестный пояс (RangeError) — показываем в поясе браузера, а не роняем страницу.
      f = new Intl.DateTimeFormat("ru-RU", opts);
    }
    formatters.set(key, f);
  }
  return f;
}

function parts(value: string | Date, timeZone?: string) {
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return null;
  const p: Record<string, string> = {};
  for (const { type, value: v } of formatter(timeZone).formatToParts(d)) p[type] = v;
  return p;
}

/**
 * Дата dd.mm.yyyy. Строка «ГГГГ-ММ-ДД» (срок задачи, даты спринта) — календарная дата без
 * времени: показываем как есть, пояс к ней не применяется, иначе западнее UTC она съехала бы
 * на день назад. Момент времени (ISO 8601 с временем) — дата в поясе пользователя.
 */
export function formatDate(value: string | Date | null | undefined, timeZone?: string): string {
  if (value == null || value === "") return "";
  if (typeof value === "string") {
    const m = DATE_ONLY.exec(value);
    if (m) return `${m[3]}.${m[2]}.${m[1]}`;
  }
  const p = parts(value, timeZone);
  return p ? `${p.day}.${p.month}.${p.year}` : "";
}

/** Время ЧЧ:ММ в поясе пользователя. */
export function formatTime(value: string | Date | null | undefined, timeZone?: string): string {
  if (value == null || value === "") return "";
  const p = parts(value, timeZone);
  return p ? `${p.hour}:${p.minute}` : "";
}

/** Дата и время «dd.mm.yyyy ЧЧ:ММ» в поясе пользователя. */
export function formatDateTime(value: string | Date | null | undefined, timeZone?: string): string {
  if (value == null || value === "") return "";
  const p = parts(value, timeZone);
  return p ? `${p.day}.${p.month}.${p.year} ${p.hour}:${p.minute}` : "";
}

/** Размер файла: «512 Б», «1,5 КБ», «12 МБ». */
export function formatBytes(n: number | null | undefined): string {
  if (n == null || !Number.isFinite(n) || n < 0) return "";
  const units = ["Б", "КБ", "МБ", "ГБ"];
  let v = n;
  let i = 0;
  // По округлённому значению: 1048575 Б — «1 МБ», а не «1024 КБ».
  while (Math.round(v) >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const s = i === 0 || v >= 10 ? String(Math.round(v)) : v.toFixed(1).replace(".", ",").replace(",0", "");
  return `${s} ${units[i]}`;
}
