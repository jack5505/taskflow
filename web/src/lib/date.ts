/**
 * Ввод даты руками (ТЗ §7: в интерфейсе даты — dd.mm.yyyy). Нативное поле `type="date"` показывает
 * формат локали браузера (в английской — mm/dd/yyyy), поэтому поле даты — текстовое, а календарь
 * открывается отдельной кнопкой.
 */

import { formatDate } from "./format";

/**
 * Значение поля даты, в котором набрано не дату (недописанная, 31.02.2026). Формы считают его
 * ошибкой ввода — не «без даты» и не прежней датой.
 */
export const INVALID_DATE = "invalid";

/** Ошибка формы для INVALID_DATE. */
export const INVALID_DATE_TEXT = "Дата в формате дд.мм.гггг";

const RU = /^(\d{1,2})\.(\d{1,2})\.(\d{4})$/;
const ISO = /^(\d{4})-(\d{2})-(\d{2})$/;

/**
 * Разбирает строку из поля даты: «5.3.2026», «05.03.2026» или вставленную «2026-03-05».
 * Возвращает дату `YYYY-MM-DD`, "" для пустой строки и null, если дата не дописана или такой нет
 * (31.02.2026).
 */
export function parseInputDate(text: string): string | null {
  const s = text.trim();
  if (s === "") return "";
  let y: number, m: number, d: number;
  const ru = RU.exec(s);
  const iso = ru ? null : ISO.exec(s);
  if (ru) [d, m, y] = [Number(ru[1]), Number(ru[2]), Number(ru[3])];
  else if (iso) [y, m, d] = [Number(iso[1]), Number(iso[2]), Number(iso[3])];
  else return null;
  if (y < 1900 || m < 1 || m > 12 || d < 1) return null;
  const last = new Date(Date.UTC(y, m, 0)).getUTCDate();
  if (d > last) return null;
  return `${String(y).padStart(4, "0")}-${String(m).padStart(2, "0")}-${String(d).padStart(2, "0")}`;
}

/** `YYYY-MM-DD` → `DD.MM.YYYY`; всё остальное — пустая строка. */
export function toInputDate(iso: string | null | undefined): string {
  return iso && ISO.test(iso) ? formatDate(iso) : "";
}
