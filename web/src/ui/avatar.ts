/** Цветов в палитре аватаров (--avatar-1 … --avatar-N в index.css). */
export const AVATAR_COLORS = 10;

/** Инициалы для аватара: «Иван Петров» → «ИП». */
export function initials(name: string | undefined): string {
  return (name ?? "")
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => [...w][0]!.toUpperCase())
    .join("");
}

/**
 * Цвета аватаров участников проекта: по порядку id, подряд по палитре — у первых AVATAR_COLORS
 * участников цвета гарантированно разные (хешем совпадения неизбежны). Порядок по id, а не по
 * списку с сервера, чтобы цвет человека не менялся от сортировки.
 */
export function memberColors(ids: readonly (string | undefined)[]): Map<string, number> {
  const sorted = [...new Set(ids.filter((id): id is string => !!id))].sort();
  return new Map(sorted.map((id, i) => [id, (i % AVATAR_COLORS) + 1]));
}

/**
 * Номер цвета аватара (1…AVATAR_COLORS) по id пользователя, когда список участников неизвестен
 * (своя аватарка в шапке): у одного человека всегда один цвет. FNV-1a по строке id.
 */
export function avatarColor(id: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return ((h >>> 0) % AVATAR_COLORS) + 1;
}
