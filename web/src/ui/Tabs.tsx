import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";

export interface TabItem<T extends string> {
  id: T;
  label: ReactNode;
}

/** Куда перейти по клавише во вкладках: стрелки по кругу, Home и End — к краям. null — не наша клавиша. */
export function nextTab(index: number, count: number, key: string): number | null {
  if (key === "ArrowRight") return (index + 1) % count;
  if (key === "ArrowLeft") return (index - 1 + count) % count;
  if (key === "Home") return 0;
  if (key === "End") return count - 1;
  return null;
}

/**
 * Вкладки по образцу WAI-ARIA: в порядке Tab — только текущая, между вкладками — стрелки,
 * Home, End (выбор сразу). Панель — tabPanel(id) у содержимого. children — то, что стоит в строке
 * вкладок после них (подсказка, период отчёта).
 */
export function Tabs<T extends string>(p: {
  label: string;
  tabs: readonly TabItem<T>[];
  value: T;
  onChange: (id: T) => void;
  className?: string;
  /** Общий префикс id вкладок и панели; по умолчанию — свой у каждого экземпляра. */
  idPrefix?: string;
  children?: ReactNode;
}) {
  const auto = useId();
  const prefix = p.idPrefix ?? auto;
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const onKey = (e: KeyboardEvent, i: number) => {
    const n = nextTab(i, p.tabs.length, e.key);
    if (n === null) return;
    e.preventDefault();
    refs.current[n]?.focus();
    p.onChange(p.tabs[n].id);
  };
  return (
    <div className={p.className ? `tabs ${p.className}` : "tabs"} role="tablist" aria-label={p.label}>
      {p.tabs.map((t, i) => {
        const on = t.id === p.value;
        return (
          <button
            key={t.id}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="tab"
            id={tabId(prefix, t.id)}
            aria-selected={on}
            aria-controls={panelId(prefix)}
            tabIndex={on ? 0 : -1}
            className={on ? "tab active" : "tab"}
            onClick={() => p.onChange(t.id)}
            onKeyDown={(e) => onKey(e, i)}
          >
            {t.label}
          </button>
        );
      })}
      {p.children}
    </div>
  );
}

const tabId = (prefix: string, id: string) => `${prefix}-tab-${id}`;
const panelId = (prefix: string) => `${prefix}-panel`;

/** Атрибуты панели вкладок: связь с текущей вкладкой и фокус по Tab (§ WAI-ARIA tabpanel). */
export function tabPanel(prefix: string, value: string) {
  return { role: "tabpanel", id: panelId(prefix), "aria-labelledby": tabId(prefix, value), tabIndex: 0 } as const;
}
