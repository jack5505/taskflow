import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";

import { Icon } from "./Icon";

export interface ToastAction {
  label: string;
  onClick: () => void;
}

interface ToastItem {
  id: number;
  kind: "success" | "error";
  text: string;
  action?: ToastAction;
}

export interface Toasts {
  /** «Сохранено», «TM-42 взята в работу»; action — «Отменить», где обратное действие есть в API. */
  success: (text: string, action?: ToastAction) => void;
  /** Текст ошибки из API. */
  error: (text: string) => void;
}

/** Сколько сообщение висит, мс; под курсором — не исчезает. */
export const TOAST_MS = 5000;

const ToastContext = createContext<Toasts | null>(null);

/** Всплывающие сообщения после действий (ТЗ §7): успех и ошибка, сами исчезают. */
export function useToast(): Toasts {
  const t = useContext(ToastContext);
  if (!t) throw new Error("useToast вне ToastProvider");
  return t;
}

function Toast({ item, onClose }: { item: ToastItem; onClose: () => void }) {
  const [hover, setHover] = useState(false);
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => {
    if (hover) return;
    const t = window.setTimeout(() => close.current(), TOAST_MS);
    return () => window.clearTimeout(t);
  }, [hover]);
  return (
    <div
      className={`toast toast-${item.kind}`}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      onFocus={() => setHover(true)}
      onBlur={() => setHover(false)}
    >
      <span className="toast-text">{item.text}</span>
      {item.action && (
        <button
          type="button"
          className="link toast-action"
          onClick={() => {
            item.action!.onClick();
            onClose();
          }}
        >
          {item.action.label}
        </button>
      )}
      <button type="button" className="link icon-button toast-close" aria-label="Закрыть" onClick={onClose}>
        <Icon name="close" size={14} />
      </button>
    </div>
  );
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const next = useRef(1);
  const push = useCallback((kind: ToastItem["kind"], text: string, action?: ToastAction) => {
    const id = next.current++;
    // Не больше трёх сразу: старые уходят.
    setItems((xs) => [...xs.slice(-2), { id, kind, text, action }]);
  }, []);
  const [api] = useState<Toasts>(() => ({
    success: (text, action) => push("success", text, action),
    error: (text) => push("error", text),
  }));
  const remove = (id: number) => setItems((xs) => xs.filter((x) => x.id !== id));

  return (
    <ToastContext.Provider value={api}>
      {children}
      {/* Две живые области: успех объявляется вежливо, ошибка — сразу. */}
      <div className="toast-stack">
        <div className="toasts" role="status" aria-live="polite">
          {items
            .filter((i) => i.kind === "success")
            .map((i) => (
              <Toast key={i.id} item={i} onClose={() => remove(i.id)} />
            ))}
        </div>
        <div className="toasts toasts-errors" role="alert" aria-live="assertive">
          {items
            .filter((i) => i.kind === "error")
            .map((i) => (
              <Toast key={i.id} item={i} onClose={() => remove(i.id)} />
            ))}
        </div>
      </div>
    </ToastContext.Provider>
  );
}
