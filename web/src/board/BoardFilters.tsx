import { useCallback, useEffect, useLayoutEffect, useRef, useState, type InputHTMLAttributes } from "react";
import type { ProjectMemberView } from "../api/client";
import {
  emptyFilter,
  isFilterEmpty,
  NO_ASSIGNEE,
  PRIORITIES,
  priorityLabels,
  TASK_TYPES,
  toggle,
  typeLabels,
  type BoardFilter,
} from "./filters";

interface Props {
  filter: BoardFilter;
  /** replace — заменить запись истории (ввод текста), иначе добавить. */
  onChange: (f: BoardFilter, replace?: boolean) => void;
  members: ProjectMemberView[];
}

/**
 * Поле ввода, которое отдаёт значение с задержкой: адрес и запрос не меняются на каждую букву.
 * Значение из адреса (кнопка «Назад», «Сбросить») подхватывается, а своё же отправленное — нет,
 * иначе запоздавший ответ затирал бы то, что пользователь уже допечатал.
 */
function DebouncedInput({ value, onCommit, ...rest }: { value: string; onCommit: (v: string) => void } & Omit<
  InputHTMLAttributes<HTMLInputElement>,
  "value" | "onChange"
>) {
  const [text, setText] = useState(value);
  const committed = useRef(value);

  useEffect(() => {
    if (value !== committed.current) {
      committed.current = value;
      setText(value);
    }
  }, [value]);

  useEffect(() => {
    if (text === committed.current) return;
    const id = setTimeout(() => {
      committed.current = text;
      onCommit(text);
    }, 300);
    return () => clearTimeout(id);
  }, [text, onCommit]);

  return <input {...rest} value={text} onChange={(e) => setText(e.target.value)} />;
}

function assigneeSummary(f: BoardFilter, members: ProjectMemberView[]): string {
  if (f.mine) return "мои";
  if (f.assignees.length === 0) return "все";
  if (f.assignees.length === 1) {
    const a = f.assignees[0];
    if (a === NO_ASSIGNEE) return "без исполнителя";
    const m = members.find((m) => m.user_id === a);
    return m?.display_name || m?.email || "1 выбран";
  }
  return `выбрано ${f.assignees.length}`;
}

/** Фильтры доски (ТЗ §7.3): исполнитель, тип, приоритет, метка, поиск. */
export function BoardFilters({ filter: f, onChange, members }: Props) {
  // «Сбросить» пересоздаёт текстовые поля: так отменяется и ещё не отправленный ввод.
  const [generation, setGeneration] = useState(0);
  // Отложенные поля срабатывают позже, чем фильтр мог смениться: берём последний, а не захваченный.
  const latest = useRef({ f, onChange });
  useLayoutEffect(() => {
    latest.current = { f, onChange };
  });
  const onQ = useCallback((q: string) => latest.current.onChange({ ...latest.current.f, q }, true), []);
  const onLabel = useCallback((label: string) => latest.current.onChange({ ...latest.current.f, label }, true), []);

  return (
    <div className="board-filters">
      <DebouncedInput
        key={`q${generation}`}
        type="search"
        className="search"
        placeholder="Поиск по ключу и названию"
        aria-label="Поиск по ключу и названию"
        maxLength={200}
        value={f.q}
        onCommit={onQ}
      />
      <details className="dropdown">
        <summary>Исполнитель: {assigneeSummary(f, members)}</summary>
        <div className="dropdown-menu">
          <label>
            <input type="checkbox" checked={f.mine} onChange={() => onChange({ ...f, mine: !f.mine })} />
            Только мои
          </label>
          <hr />
          <label className={f.mine ? "muted" : undefined}>
            <input
              type="checkbox"
              disabled={f.mine}
              checked={f.assignees.includes(NO_ASSIGNEE)}
              onChange={() => onChange({ ...f, assignees: toggle(f.assignees, NO_ASSIGNEE) })}
            />
            Без исполнителя
          </label>
          {members.map((m) => (
            <label key={m.user_id} className={f.mine ? "muted" : undefined}>
              <input
                type="checkbox"
                disabled={f.mine}
                checked={f.assignees.includes(m.user_id!)}
                onChange={() => onChange({ ...f, assignees: toggle(f.assignees, m.user_id!) })}
              />
              {m.display_name || m.email}
              {m.is_active === false && <span className="muted"> (деактивирован)</span>}
            </label>
          ))}
        </div>
      </details>
      <div className="chips" role="group" aria-label="Тип">
        {TASK_TYPES.map((t) => (
          <button
            key={t}
            type="button"
            className={f.types.includes(t) ? "chip active" : "chip"}
            aria-pressed={f.types.includes(t)}
            onClick={() => onChange({ ...f, types: toggle(f.types, t) })}
          >
            {typeLabels[t]}
          </button>
        ))}
      </div>
      <div className="chips" role="group" aria-label="Приоритет">
        {PRIORITIES.map((p) => (
          <button
            key={p}
            type="button"
            className={f.priorities.includes(p) ? "chip active" : "chip"}
            aria-pressed={f.priorities.includes(p)}
            onClick={() => onChange({ ...f, priorities: toggle(f.priorities, p) })}
          >
            {priorityLabels[p]}
          </button>
        ))}
      </div>
      <DebouncedInput
        key={`label${generation}`}
        className="label-filter"
        placeholder="Метка"
        aria-label="Метка"
        maxLength={30}
        value={f.label}
        onCommit={onLabel}
      />
      {!isFilterEmpty(f) && (
        <button type="button" onClick={() => {
            setGeneration((g) => g + 1);
            onChange(emptyFilter);
          }}>
          Сбросить
        </button>
      )}
    </div>
  );
}
