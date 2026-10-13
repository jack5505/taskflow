import { useRef, useState, type Ref } from "react";

import { parseInputDate, toInputDate } from "../lib/date";

export interface DateFieldProps {
  /** Дата `YYYY-MM-DD` или "". */
  value: string;
  /**
   * Новая дата `YYYY-MM-DD` или "" (поле очищено). Недописанная или несуществующая дата не
   * передаётся: поле подсвечивается при уходе фокуса. `picked` — выбрана в календаре, а не введена.
   */
  onChange: (value: string, picked: boolean) => void;
  min?: string;
  max?: string;
  required?: boolean;
  disabled?: boolean;
  id?: string;
  "aria-label"?: string;
  "aria-invalid"?: boolean;
  "data-testid"?: string;
  autoFocus?: boolean;
  ref?: Ref<HTMLInputElement>;
  onBlur?: () => void;
  onEnter?: () => void;
}

/**
 * Поле даты в формате dd.mm.yyyy (ТЗ §7) независимо от языка браузера: текст плюс кнопка
 * календаря, которая открывает нативный выбор даты скрытого поля `type="date"`.
 */
export function DateField(p: DateFieldProps) {
  const [text, setText] = useState(() => toInputDate(p.value));
  const [shown, setShown] = useState(p.value);
  const [bad, setBad] = useState(false);
  const picker = useRef<HTMLInputElement>(null);

  // Значение поменялось снаружи (сброс формы, ответ сервера) — показать его, если оно не то,
  // что сейчас набрано.
  if (p.value !== shown) {
    setShown(p.value);
    if (parseInputDate(text) !== p.value) {
      setText(toInputDate(p.value));
      setBad(false);
    }
  }

  const type = (s: string) => {
    setText(s);
    const iso = parseInputDate(s);
    if (iso !== null) {
      setBad(false);
      if (iso !== p.value) p.onChange(iso, false);
    }
  };

  const openPicker = () => {
    const el = picker.current;
    if (!el) return;
    try {
      el.showPicker();
    } catch {
      el.focus();
      el.click();
    }
  };

  return (
    <span className="date-field">
      <input
        type="text"
        inputMode="numeric"
        autoComplete="off"
        placeholder="дд.мм.гггг"
        maxLength={10}
        ref={p.ref}
        id={p.id}
        value={text}
        required={p.required}
        disabled={p.disabled}
        autoFocus={p.autoFocus}
        aria-label={p["aria-label"]}
        aria-invalid={bad || p["aria-invalid"] || undefined}
        data-testid={p["data-testid"]}
        onChange={(e) => type(e.target.value)}
        onBlur={() => {
          setBad(parseInputDate(text) === null);
          p.onBlur?.();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            setBad(parseInputDate(text) === null);
            p.onEnter?.();
          }
        }}
      />
      <button
        type="button"
        className="date-field-pick"
        aria-label="Выбрать в календаре"
        title="Выбрать в календаре"
        disabled={p.disabled}
        onClick={openPicker}
      >
        <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
          <rect x="2" y="3" width="12" height="11" rx="2" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M2 6.5h12M5.5 1.5v3M10.5 1.5v3" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      </button>
      <input
        ref={picker}
        type="date"
        className="date-field-native"
        tabIndex={-1}
        aria-hidden="true"
        value={p.value}
        min={p.min}
        max={p.max}
        disabled={p.disabled}
        onChange={(e) => {
          setText(toInputDate(e.target.value));
          setBad(false);
          if (e.target.value !== p.value) p.onChange(e.target.value, true);
        }}
      />
    </span>
  );
}
