/** Цветов в палитре аватаров (--avatar-1 … --avatar-N в index.css). */
export const AVATAR_COLORS = 8;

/**
 * Номер цвета аватара (1…AVATAR_COLORS) по id пользователя: у одного человека всегда один цвет,
 * у разных — чаще всего разные. FNV-1a по строке id.
 */
export function avatarColor(id: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return ((h >>> 0) % AVATAR_COLORS) + 1;
}
