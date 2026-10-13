import { useEffect, useRef, type ReactNode } from "react";

/**
 * Модальное окно на нативном `<dialog>` (showModal): фокус не уходит за окно, остальная страница
 * недоступна, после закрытия фокус возвращается туда, где был. Escape и щелчок по фону вызывают
 * onCancel — закрывать или нет, решает владелец (например, спросить про несохранённое).
 */
export function Modal(p: { labelledBy: string; onCancel: () => void; className?: string; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  const cancel = useRef(p.onCancel);
  cancel.current = p.onCancel;

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const back = document.activeElement as HTMLElement | null;
    if (!d.open) d.showModal();
    const onCancel = (e: Event) => {
      e.preventDefault(); // Escape: окно закрывает владелец, а не браузер
      cancel.current();
    };
    d.addEventListener("cancel", onCancel);
    return () => {
      d.removeEventListener("cancel", onCancel);
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
      onMouseDown={(e) => e.target === e.currentTarget && cancel.current()}
    >
      {p.children}
    </dialog>
  );
}
