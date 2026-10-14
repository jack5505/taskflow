import {
  KeyboardSensor,
  MouseSensor,
  TouchSensor,
  useSensor,
  useSensors,
  type DraggableAttributes,
  type DraggableSyntheticListeners,
} from "@dnd-kit/core";
import { useSyncExternalStore } from "react";

/**
 * Клавиатурное перетаскивание (dnd-kit) начинается пробелом, а не Enter: в карточке и строке
 * фокус стоит на ссылке задачи, и Enter должен её открывать. Escape — отмена, пробел или Enter —
 * положить.
 */
export const keyboardCodes = { start: ["Space"], cancel: ["Escape"], end: ["Space", "Enter"] };

/**
 * Датчики перетаскивания доски и бэклога (ТЗ §7). Мышь — после сдвига на 5 px: щелчок по кнопке
 * на карточке не начинает перетаскивание. Палец — после долгого нажатия (250 мс, дрожание до
 * 8 px): обычное касание прокручивает страницу и колонки. Клавиатура — пробелом.
 */
export function useDragSensors() {
  return useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 5 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 250, tolerance: 8 } }),
    useSensor(KeyboardSensor, { keyboardCodes }),
  );
}

/**
 * Перетаскиваемый элемент без своей роли кнопки: внутри него ссылка, а кнопка со ссылкой внутри —
 * вложенные интерактивные элементы. Обработчики — на обёртке (до неё всплывают события ссылки),
 * подсказки dnd-kit для чтецов — на самой ссылке.
 */
export function splitDraggable(attributes: DraggableAttributes, listeners: DraggableSyntheticListeners, roledescription: string) {
  // Ссылке — только подсказка и описание: aria-pressed и aria-disabled для роли link недопустимы.
  return {
    wrapper: { ...listeners },
    link: { "aria-describedby": attributes["aria-describedby"], "aria-roledescription": roledescription },
  };
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
