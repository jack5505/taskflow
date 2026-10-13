import { initials } from "../board/cards";
import { avatarColor } from "./avatar";

/**
 * Аватар исполнителя: инициалы на цвете, постоянном для человека. Без id — пустой кружок
 * «Без исполнителя».
 */
export function Avatar({ id, name, size = "md" }: { id?: string; name?: string; size?: "sm" | "md" }) {
  if (!id) return <span className={`avatar avatar-${size} empty`} title="Без исполнителя" aria-label="Без исполнителя" role="img" />;
  const label = name || "Исполнитель";
  return (
    <span className={`avatar avatar-${size} avatar-c${avatarColor(id)}`} title={label} aria-label={`Исполнитель: ${label}`} role="img">
      {initials(name) || "?"}
    </span>
  );
}
