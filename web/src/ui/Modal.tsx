import { useEffect, useRef, useSyncExternalStore, type ReactNode } from "react";

// Открытые модальные окна по порядку: всё вне верхнего браузер делает недоступным (inert), поэтому
// сообщения (ui/Toast) рисуются внутри него.
let stack: HTMLDialogElement[] = [];
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());

const FOCUSABLE =
  'a[href], button:not(:disabled), input:not(:disabled):not([type="hidden"]), select:not(:disabled), textarea:not(:disabled), summary, [tabindex]';

/** Элементы окна в порядке Tab: видимые и не выключенные из него (tabindex=-1, как у неактивных вкладок). */
function tabbable(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.tabIndex >= 0 && el.offsetParent !== null);
}

/** Верхнее открытое модальное окно или null. */
export function useTopModal(): HTMLDialogElement | null {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => stack.at(-1) ?? null,
  );
}

/**
 * Модальное окно на нативном `<dialog>` (showModal): фокус не уходит за окно, остальная страница
 * недоступна, после закрытия фокус возвращается туда, где был. Escape и щелчок по фону вызывают
 * onCancel — закрывать или нет, решает владелец (например, спросить про несохранённое).
 */
/**
 * Первое поле окна — атрибутом data-autofocus, а не autoFocus: React ставит фокус до showModal(),
 * пока окно ещё закрыто, и фокус теряется. closeOnBackdrop={false} — окно, которое нельзя закрыть
 * случайным щелчком мимо (конфликт версий: закрытие выбрасывает правки).
 */
export function Modal(p: {
  labelledBy: string;
  onCancel: () => void;
  className?: string;
  closeOnBackdrop?: boolean;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const cancel = useRef(p.onCancel);
  useEffect(() => {
    cancel.current = p.onCancel;
  });

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const back = document.activeElement as HTMLElement | null;
    if (!d.open) d.showModal();
    d.querySelector<HTMLElement>("[data-autofocus]")?.focus();
    stack = [...stack, d];
    emit();
    const onCancel = (e: Event) => {
      e.preventDefault(); // Escape: окно закрывает владелец, а не браузер
      cancel.current();
    };
    // Chrome при частых Escape шлёт неотменяемый cancel и закрывает окно сам, а владелец окно ещё
    // держит (например, идёт запрос). Пока окно смонтировано, оно открыто: открываем снова.
    const onClose = () => {
      if (!d.isConnected) return;
      d.showModal();
      cancel.current();
    };
    d.addEventListener("cancel", onCancel);
    d.addEventListener("close", onClose);
    return () => {
      d.removeEventListener("cancel", onCancel);
      d.removeEventListener("close", onClose);
      stack = stack.filter((x) => x !== d);
      emit();
      if (d.open) d.close();
      // Окно размонтировано (а не закрыто браузером) — фокус возвращаем сами.
      if (back?.isConnected) back.focus();
    };
  }, []);

  return (
    <dialog
      ref={ref}
      className={p.className ? `modal-dialog ${p.className}` : "modal-dialog"}
      aria-labelledby={p.labelledBy}
      // Сам <dialog> без отступов, всё содержимое — в .modal-body: щелчок по самому <dialog> —
      // это щелчок по фону, а не по полям или полосе прокрутки окна.
      onMouseDown={(e) => p.closeOnBackdrop !== false && e.target === e.currentTarget && cancel.current()}
      onKeyDown={(e) => {
        // Tab по кругу внутри окна: нативный <dialog> отпускает фокус в интерфейс браузера.
        if (e.key !== "Tab") return;
        const items = tabbable(e.currentTarget);
        if (items.length === 0) return;
        const first = items[0];
        const last = items[items.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first.focus();
        }
      }}
    >
      <div className="modal-body">{p.children}</div>
    </dialog>
  );
}
