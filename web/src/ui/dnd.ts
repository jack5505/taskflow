import type { DraggableAttributes, DraggableSyntheticListeners } from "@dnd-kit/core";
import { useSyncExternalStore } from "react";

/**
 * Клавиатурное перетаскивание (dnd-kit) начинается пробелом, а не Enter: в карточке и строке
 * фокус стоит на ссылке задачи, и Enter должен её открывать. Escape — отмена, пробел или Enter —
 * положить.
 */
export const keyboardCodes = { start: ["Space"], cancel: ["Escape"], end: ["Space", "Enter"] };

/**
 * Перетаскиваемый элемент без своей роли кнопки: внутри него ссылка, а кнопка со ссылкой внутри —
 * вложенные интерактивные элементы. Обработчики — на обёртке (до неё всплывают события ссылки),
 * подсказки dnd-kit для чтецов — на самой ссылке.
 */
export function splitDraggable(attributes: DraggableAttributes, listeners: DraggableSyntheticListeners, roledescription: string) {
  const { role: _role, tabIndex: _tabIndex, ...aria } = attributes;
  return { wrapper: { ...listeners }, link: { ...aria, "aria-roledescription": roledescription } };
}

const query = "(prefers-reduced-motion: reduce)";

/** Пользователь просит меньше движения: без анимации «бросания» карточки. */
export function useReducedMotion(): boolean {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia(query);
      m.addEventListener("change", cb);
      return () => m.removeEventListener("change", cb);
    },
    () => window.matchMedia(query).matches,
  );
}
