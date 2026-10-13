import { createContext, useCallback, useContext, useId, useRef, useState, type ReactNode } from "react";

import { Modal } from "./Modal";

export interface ConfirmOptions {
  /** Вопрос: «Удалить задачу TM-42?» */
  title: string;
  /** Что произойдёт: «Задача пропадёт с доски…». */
  body?: ReactNode;
  /** Кнопка действия глаголом: «Удалить», «Исключить». */
  confirmLabel: string;
  /** Необратимое или разрушительное действие — красная кнопка. */
  danger?: boolean;
}

type Ask = (o: ConfirmOptions) => Promise<boolean>;
const ConfirmContext = createContext<Ask | null>(null);

/**
 * Подтверждение своим окном вместо `window.confirm` (ТЗ §7): текст последствий и кнопка-действие.
 * `if (await confirm({ … })) remove.mutate()`.
 */
export function useConfirm(): Ask {
  const ask = useContext(ConfirmContext);
  if (!ask) throw new Error("useConfirm вне ConfirmProvider");
  return ask;
}

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState<ConfirmOptions | null>(null);
  const resolve = useRef<((ok: boolean) => void) | null>(null);
  const id = useId();

  const ask = useCallback<Ask>((o) => {
    resolve.current?.(false); // второй вопрос поверх первого — первый считается отменённым
    setOpen(o);
    return new Promise<boolean>((r) => {
      resolve.current = r;
    });
  }, []);

  const finish = (ok: boolean) => {
    resolve.current?.(ok);
    resolve.current = null;
    setOpen(null);
  };

  return (
    <ConfirmContext.Provider value={ask}>
      {children}
      {open && (
        <Modal labelledBy={`${id}-title`} onCancel={() => finish(false)} className="confirm">
          <form
            method="dialog"
            className="form"
            onSubmit={(e) => {
              e.preventDefault();
              finish(true);
            }}
          >
            <h2 id={`${id}-title`}>{open.title}</h2>
            {open.body && <div className="confirm-body">{open.body}</div>}
            <div className="actions">
              <button type="submit" className={open.danger ? "danger-solid" : "primary"} autoFocus>
                {open.confirmLabel}
              </button>
              <button type="button" onClick={() => finish(false)}>
                Отмена
              </button>
            </div>
          </form>
        </Modal>
      )}
    </ConfirmContext.Provider>
  );
}
