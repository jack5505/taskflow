import { useState, type FormEvent, type ReactNode } from "react";
import type { SprintView } from "../api/client";
import { formatDuration } from "../lib/duration";
import { BACKLOG, sprintDatesError, totals } from "./logic";

function Modal({ id, title, onCancel, onSubmit, children }: {
  id: string;
  title: string;
  onCancel: () => void;
  onSubmit: (e: FormEvent) => void;
  children: ReactNode;
}) {
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onCancel()}>
      <form
        className="form panel modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        onSubmit={onSubmit}
        onKeyDown={(e) => e.key === "Escape" && onCancel()}
      >
        <h2 id={id}>{title}</h2>
        {children}
      </form>
    </div>
  );
}

function Actions({ pending, label, onCancel }: { pending: boolean; label: string; onCancel: () => void }) {
  return (
    <div className="actions">
      <button type="submit" className="primary" disabled={pending}>
        {pending ? "Сохраняем…" : label}
      </button>
      <button type="button" onClick={onCancel}>
        Отмена
      </button>
    </div>
  );
}

export interface SprintFields {
  name: string;
  goal: string;
  start: string;
  end: string;
}

interface FormProps {
  pending: boolean;
  /** Ошибка сервера по последней отправке. */
  error: string;
  onCancel: () => void;
  /** Правка формы — ошибку прошлой отправки пора убрать. */
  onEdit: () => void;
}

/** «Создать спринт» (§5.1): название (пусто — «Спринт N» от сервера), цель, даты необязательны. */
export function CreateSprintDialog(p: FormProps & { onSubmit: (f: SprintFields) => void }) {
  const [f, setF] = useState<SprintFields>({ name: "", goal: "", start: "", end: "" });
  const [localError, setLocalError] = useState("");
  const set = (k: keyof SprintFields) => (e: { target: { value: string } }) => {
    setF({ ...f, [k]: e.target.value });
    setLocalError("");
    p.onEdit();
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const err = sprintDatesError(f.start, f.end, false);
    setLocalError(err);
    if (!err) p.onSubmit(f);
  };
  const error = localError || p.error;
  return (
    <Modal id="sprint-create-title" title="Новый спринт" onCancel={p.onCancel} onSubmit={submit}>
      <label>
        Название
        <input autoFocus maxLength={100} value={f.name} onChange={set("name")} />
        <span className="hint">Пусто — «Спринт N» по порядку</span>
      </label>
      <label>
        Цель
        <textarea rows={3} maxLength={1000} value={f.goal} onChange={set("goal")} />
      </label>
      <div className="date-range">
        <label>
          Начало
          <input type="date" value={f.start} onChange={set("start")} />
        </label>
        <label>
          Окончание
          <input type="date" value={f.end} onChange={set("end")} />
        </label>
      </div>
      <span className="hint">Даты можно задать и при запуске</span>
      {error && <p role="alert">{error}</p>}
      <Actions pending={p.pending} label="Создать" onCancel={p.onCancel} />
    </Modal>
  );
}

/**
 * «Запустить спринт» (§5.2): цель и даты (обе обязательны, окончание позже начала, не дальше 42
 * дней), сводка объёма на старте с числом задач без оценки. Остальное проверяет сервер (409).
 */
export function StartSprintDialog(
  p: FormProps & {
    sprint: SprintView;
    scope: ReturnType<typeof totals>;
    defaults: { start: string; end: string };
    onSubmit: (f: SprintFields) => void;
  },
) {
  const [f, setF] = useState<SprintFields>({
    name: p.sprint.name ?? "",
    goal: p.sprint.goal ?? "",
    start: p.sprint.start_date || p.defaults.start,
    end: p.sprint.end_date || p.defaults.end,
  });
  const [localError, setLocalError] = useState("");
  const set = (k: keyof SprintFields) => (e: { target: { value: string } }) => {
    setF({ ...f, [k]: e.target.value });
    setLocalError("");
    p.onEdit();
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    let err = sprintDatesError(f.start, f.end, true);
    if (!err && p.scope.count === 0) err = "В спринте нет задач — перетащите их из бэклога";
    setLocalError(err);
    if (!err) p.onSubmit(f);
  };
  const error = localError || p.error;
  return (
    <Modal id="sprint-start-title" title={`Запустить «${p.sprint.name}»`} onCancel={p.onCancel} onSubmit={submit}>
      <p className="muted">
        Задач: {p.scope.count}, оценка {formatDuration(p.scope.estimate)}
        {p.scope.withoutEstimate > 0 && `, без оценки: ${p.scope.withoutEstimate}`}
      </p>
      <label>
        Цель
        <textarea autoFocus rows={3} maxLength={1000} value={f.goal} onChange={set("goal")} />
      </label>
      <div className="date-range">
        <label>
          Начало
          <input type="date" required value={f.start} onChange={set("start")} />
        </label>
        <label>
          Окончание
          <input type="date" required value={f.end} onChange={set("end")} />
        </label>
      </div>
      <span className="hint">Не длиннее 42 дней</span>
      {error && <p role="alert">{error}</p>}
      <Actions pending={p.pending} label="Запустить" onCancel={p.onCancel} />
    </Modal>
  );
}

/**
 * «Завершить спринт» (§5.4): сводка готовых и незакрытых и один выбор для всех незакрытых —
 * в бэклог или в запланированный спринт. Готовые и отменённые остаются в спринте.
 */
export function CloseSprintDialog(p: {
  sprint: SprintView;
  summary: { done: number; open: number; cancelled: number };
  planned: SprintView[];
  pending: boolean;
  error: string;
  onCancel: () => void;
  onEdit: () => void;
  onSubmit: (moveTo: string) => void;
}) {
  const [moveTo, setMoveTo] = useState(BACKLOG);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    p.onSubmit(moveTo);
  };
  return (
    <Modal id="sprint-close-title" title={`Завершить «${p.sprint.name}»`} onCancel={p.onCancel} onSubmit={submit}>
      <p>
        Готово: {p.summary.done}, незакрыто: {p.summary.open}
        {p.summary.cancelled > 0 && `, отменено: ${p.summary.cancelled}`}
      </p>
      {p.summary.open > 0 ? (
        <label>
          Куда перенести незакрытые задачи
          <select
            autoFocus
            value={moveTo}
            onChange={(e) => {
              setMoveTo(e.target.value);
              p.onEdit();
            }}
          >
            <option value={BACKLOG}>В бэклог</option>
            {p.planned.map((s) => (
              <option key={s.id} value={s.id}>
                В спринт «{s.name}»
              </option>
            ))}
          </select>
        </label>
      ) : (
        <p className="muted">Незакрытых задач нет.</p>
      )}
      <p className="hint">Завершённый спринт нельзя открыть снова.</p>
      {p.error && <p role="alert">{p.error}</p>}
      <Actions pending={p.pending} label="Завершить" onCancel={p.onCancel} />
    </Modal>
  );
}
