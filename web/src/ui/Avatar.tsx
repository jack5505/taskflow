import { createContext, useContext } from "react";

import { avatarColor, initials } from "./avatar";

/**
 * Цвета аватаров участников проекта (memberColors) — страница проекта кладёт их сюда, и у
 * участников цвета разные. Без провайдера (шапка) — цвет по id.
 */
export const AvatarColors = createContext<ReadonlyMap<string, number> | null>(null);

/**
 * Аватар: инициалы на цвете человека. Без id — пустой кружок «Без исполнителя». decorative —
 * имя уже написано рядом текстом, чтецу второй раз его не читать.
 */
export function Avatar(p: { id?: string; name?: string; size?: "sm" | "md"; decorative?: boolean }) {
  const colors = useContext(AvatarColors);
  const size = p.size ?? "md";
  const a11y = (label: string) => (p.decorative ? { "aria-hidden": true } : { role: "img", "aria-label": label });
  if (!p.id) return <span className={`avatar avatar-${size} empty`} title="Без исполнителя" {...a11y("Без исполнителя")} />;
  const label = p.name || "Исполнитель";
  const color = colors?.get(p.id) ?? avatarColor(p.id);
  return (
    <span className={`avatar avatar-${size} avatar-c${color}`} title={label} {...a11y(`Исполнитель: ${label}`)}>
      {initials(p.name) || "?"}
    </span>
  );
}
