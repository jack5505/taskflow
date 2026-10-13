import { useRef, useState, type Ref } from "react";

import { INVALID_DATE, parseInputDate, toInputDate } from "../lib/date";

export interface DateFieldProps {
  /** Дата `YYYY-MM-DD`, "" или INVALID_DATE (то, что пришло из onChange). */
  value: string;
  /**
   * Новая дата `YYYY-MM-DD`, "" (поле очищено) или INVALID_DATE — набрано не дату; форма решает,
   * ошибка это или поле просто вернуть. `picked` — выбрана в календаре, а не введена.
   */
  onChange: (value: string, picked: boolean) => void;
  /** При уходе фокуса вернуть текст к value, если набрано не то (фильтры, где нет кнопки «Сохранить»). */
  revertOnBlur?: boolean;
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
// Календарь — только через showPicker (Chrome 99+, Firefox 101+, Safari 16+): программный click по
// скрытому полю календарь не открывает, и без showPicker кнопка не показывается.
const canPick = typeof HTMLInputElement !== "undefined" && "showPicker" in HTMLInputElement.prototype;

/** Значение для onChange из набранного текста. */
const fromText = (s: string) => parseInputDate(s) ?? INVALID_DATE;

export function DateField(p: DateFieldProps) {
  const [text, setText] = useState(() => toInputDate(p.value));
  const [shown, setShown] = useState(p.value);
  const [bad, setBad] = useState(false);
  const picker = useRef<HTMLInputElement>(null);

  // Значение поменялось снаружи (сброс формы, ответ сервера) — показать его, если оно не то,
  // что сейчас набрано. INVALID_DATE пришло от нас же: текст не трогаем.
  if (p.value !== shown) {
    setShown(p.value);
    if (p.value !== INVALID_DATE && fromText(text) !== p.value) {
      setText(toInputDate(p.value));
      setBad(false);
    }
  }

  const type = (s: string) => {
    setText(s);
    const v = fromText(s);
    if (v !== INVALID_DATE) setBad(false);
    if (v !== p.value) p.onChange(v, false);
  };

  const leave = () => {
    if (p.revertOnBlur && fromText(text) !== p.value) {
      setText(toInputDate(p.value));
      setBad(false);
    } else setBad(fromText(text) === INVALID_DATE);
  };

  const openPicker = () => {
    try {
      picker.current?.showPicker();
    } catch {
      // Браузер отказал (например, поле отключено) — остаётся ручной ввод.
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
          leave();
          p.onBlur?.();
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            leave();
            p.onEnter?.();
          }
        }}
      />
      {canPick && (
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
      )}
      <input
        ref={picker}
        type="date"
        className="date-field-native"
        tabIndex={-1}
        aria-hidden="true"
        value={p.value === INVALID_DATE ? "" : p.value}
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
