import { useRef, useState, type ReactNode } from "react";
import type { ProjectActiveSprint, ProjectMemberView, TaskTask } from "../api/client";
import type { ValueContext } from "./history";
import { PRIORITIES, TASK_TYPES, priorityLabels, typeLabels } from "../board/filters";
import { formatDuration, parseDuration } from "../lib/duration";
import { formatDate, formatDateTime } from "../lib/format";
import { parseLabels, type Edits } from "./patch";
import type { TaskPerms } from "./perms";

/** Итог сохранения: при ошибке редактор остаётся открытым, при конфликте правку забирает окно конфликта. */
export type SaveResult = "saved" | "conflict" | "error";
/**
 * Сохранить правку. base — версия задачи, которую пользователь видел, начиная правку: её
 * version уходит в If-Match, чтобы чужое изменение, пришедшее за это время, дало 409, а не
 * перезаписалось молча.
 */
export type Save = (edits: Edits, base: TaskTask) => Promise<SaveResult>;

interface InlineProps {
  /** Текст в поле ввода при открытии. */
  initial: string;
  /** Показ без редактирования. */
  children: ReactNode;
  /** Задача на экране: версия на момент открытия поля уходит в If-Match. */
  task: TaskTask;
  editable: boolean;
  /** Идёт другое сохранение: открыть поле нельзя. */
  disabled?: boolean;
  label: string;
  placeholder?: string;
  /** "" — годится, иначе текст ошибки. */
  validate?: (v: string) => string;
  onSave: (v: string, base: TaskTask) => Promise<SaveResult>;
  className?: string;
}

/** Значение, которое правится по щелчку: Enter или уход фокуса — сохранить, Escape — отмена. */
export function InlineEdit(p: InlineProps) {
  const [draft, setDraft] = useState<string | null>(null);
  const [base, setBase] = useState<TaskTask>(p.task);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // Escape закрывает поле; уход фокуса при закрытии не должен сохранять.
  const cancelled = useRef(false);
  // Черновик, сохранение которого не удалось: уход фокуса его не отправляет повторно (Enter — да).
  const failed = useRef<string | null>(null);

  if (!p.editable) return <span className={p.className}>{p.children}</span>;
  if (draft === null)
    return (
      <button
        type="button"
        className={`inline-edit ${p.className ?? ""}`}
        title="Изменить"
        aria-label={`${p.label}: изменить`}
        disabled={p.disabled}
        onClick={() => {
          setError("");
          cancelled.current = false;
          failed.current = null;
          setBase(p.task);
          setDraft(p.initial);
        }}
      >
        {p.children}
      </button>
    );

  const commit = async (fromBlur = false) => {
    if (busy || cancelled.current || (fromBlur && draft === failed.current)) return;
    const err = p.validate?.(draft) ?? "";
    setError(err);
    if (err) return;
    setBusy(true);
    const r = await p.onSave(draft, base);
    setBusy(false);
    if (r !== "error") setDraft(null);
    else failed.current = draft;
  };
  return (
    <span className="inline-editor">
      <input
        autoFocus
        aria-label={p.label}
        className={p.className}
        value={draft}
        placeholder={p.placeholder}
        // readOnly, а не disabled: при ошибке сохранения фокус остаётся в поле, Escape работает.
        readOnly={busy}
        aria-invalid={!!error}
        onChange={(e) => {
          setDraft(e.target.value);
          setError("");
        }}
        onBlur={() => void commit(true)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            void commit();
          }
          if (e.key === "Escape") {
            cancelled.current = true;
            setDraft(null);
          }
        }}
      />
      {error && <span role="alert" className="field-error">{error}</span>}
    </span>
  );
}

/**
 * Срок: сохраняется при уходе фокуса или Enter, а не на каждое изменение — иначе ручной ввод
 * года отправил бы PATCH на каждую цифру.
 */
function DateInput(p: { task: TaskTask; disabled: boolean; onSave: (v: string, base: TaskTask) => void }) {
  const value = p.task.due_date ?? "";
  const [draft, setDraft] = useState<string | null>(null);
  const [base, setBase] = useState<TaskTask>(p.task);
  const commit = (el: HTMLInputElement) => {
    // Недописанная дата (validity.badInput) даёт value "" — это не очистка срока.
    if (draft !== null && draft !== value && !el.validity.badInput) p.onSave(draft, base);
    setDraft(null);
  };
  return (
    <input
      type="date"
      aria-label="Срок"
      value={draft ?? value}
      disabled={p.disabled}
      onChange={(e) => {
        if (draft === null) setBase(p.task);
        setDraft(e.target.value);
      }}
      onBlur={(e) => commit(e.currentTarget)}
      onKeyDown={(e) => e.key === "Enter" && commit(e.currentTarget)}
    />
  );
}

interface Props {
  task: TaskTask;
  perms: TaskPerms;
  members: ProjectMemberView[];
  /** Имена участников — те же, что в истории. */
  ctx: ValueContext;
  activeSprint?: ProjectActiveSprint;
  timeZone?: string;
  saving: boolean;
  onSave: Save;
}


/** Правая панель карточки (§7.5, §6.2): поля задачи, учёт времени и даты. */
export function TaskFields({ task: t, perms, members, ctx, activeSprint, timeZone, saving, onSave }: Props) {
  const name = (id: string | undefined) => (id ? (ctx.userName(id) ?? "Пользователь не в проекте") : "");
  const spent = t.spent_minutes ?? 0;
  const estimate = t.estimate_minutes;
  const left = estimate ? Math.max(estimate - spent, 0) : undefined;
  const over = estimate ? spent - estimate : 0;
  const progress = estimate ? Math.min(100, Math.round((spent / estimate) * 100)) : 0;
  const sprint = !t.sprint_id
    ? "Бэклог"
    : t.sprint_id === activeSprint?.id
      ? `${activeSprint.name ?? "Активный спринт"} (активный)`
      : "Спринт (не активный)";
  const assignable = members.filter((m) => m.is_active !== false);

  return (
    <dl className="task-fields">
      <dt>Тип</dt>
      <dd>
        {perms.edit ? (
          <select
            aria-label="Тип"
            value={t.type ?? "task"}
            disabled={saving}
            onChange={(e) => void onSave({ type: e.target.value as TaskTask["type"] }, t)}
          >
            {TASK_TYPES.map((v) => (
              <option key={v} value={v}>
                {typeLabels[v]}
              </option>
            ))}
          </select>
        ) : (
          typeLabels[t.type ?? "task"]
        )}
      </dd>

      <dt>Приоритет</dt>
      <dd>
        {perms.edit ? (
          <select
            aria-label="Приоритет"
            value={t.priority ?? "medium"}
            disabled={saving}
            onChange={(e) => void onSave({ priority: e.target.value as TaskTask["priority"] }, t)}
          >
            {PRIORITIES.map((v) => (
              <option key={v} value={v}>
                {priorityLabels[v]}
              </option>
            ))}
          </select>
        ) : (
          priorityLabels[t.priority ?? "medium"]
        )}
      </dd>

      <dt>Исполнитель</dt>
      <dd>
        {perms.assign ? (
          <select
            aria-label="Исполнитель"
            value={t.assignee_id ?? ""}
            disabled={saving}
            onChange={(e) => void onSave({ assignee_id: e.target.value || undefined }, t)}
          >
            {(perms.unassign || !t.assignee_id) && <option value="">— не назначен —</option>}
            {/* Текущий исполнитель остаётся в списке, даже если деактивирован. */}
            {t.assignee_id && !assignable.some((m) => m.user_id === t.assignee_id) && (
              <option value={t.assignee_id}>{name(t.assignee_id)}</option>
            )}
            {assignable.map((m) => (
              <option key={m.user_id} value={m.user_id}>
                {m.display_name || m.email}
              </option>
            ))}
          </select>
        ) : (
          name(t.assignee_id) || <span className="muted">не назначен</span>
        )}
      </dd>

      <dt>Автор</dt>
      <dd>{name(t.reporter_id) || "—"}</dd>

      <dt>Спринт</dt>
      <dd>{sprint}</dd>

      <dt>Срок</dt>
      <dd>
        {perms.edit ? (
          <DateInput task={t} disabled={saving} onSave={(v, base) => void onSave({ due_date: v }, base)} />
        ) : (
          formatDate(t.due_date) || "—"
        )}
      </dd>

      <dt>Метки</dt>
      <dd>
        <InlineEdit
          task={t}
          disabled={saving}
          editable={perms.edit}
          label="Метки"
          placeholder="через запятую"
          initial={(t.labels ?? []).join(", ")}
          validate={(v) => {
            const l = parseLabels(v);
            if (l.length > 10) return "Не больше 10 меток";
            if (l.some((x) => [...x].length > 30)) return "Метка — до 30 символов";
            return "";
          }}
          onSave={(v, base) => onSave({ labels: parseLabels(v) }, base)}
        >
          {t.labels?.length ? (
            t.labels.map((l) => (
              <span key={l} className="badge">
                {l}
              </span>
            ))
          ) : (
            <span className="muted">нет</span>
          )}
        </InlineEdit>
      </dd>

      <dt>Оценка</dt>
      <dd>
        <InlineEdit
          task={t}
          disabled={saving}
          editable={perms.estimate}
          label="Оценка"
          placeholder="2h 30m"
          initial={formatDuration(estimate)}
          validate={(v) => {
            if (!v.trim()) return "";
            const m = parseDuration(v);
            if (m === null) return "Формат: 2h 30m, 45m, 3h, 1d";
            if (m < 1 || m > 60000) return "От 1m до 1000h";
            return "";
          }}
          onSave={(v, base) => onSave({ estimate_minutes: v.trim() ? (parseDuration(v) ?? undefined) : undefined }, base)}
        >
          {formatDuration(estimate) || <span className="muted">нет</span>}
        </InlineEdit>
      </dd>

      <dt>Списано</dt>
      <dd>{formatDuration(spent)}</dd>

      <dt>Остаток</dt>
      <dd>
        {left === undefined ? "—" : formatDuration(left)}
        {over > 0 && <span className="overdue"> перерасход {formatDuration(over)}</span>}
      </dd>

      <dd className="progress-row">
        <div
          className={over > 0 ? "progress over" : "progress"}
          role="progressbar"
          aria-label="Списано от оценки"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={progress}
        >
          <span style={{ width: `${progress}%` }} />
        </div>
      </dd>

      <dt>Создана</dt>
      <dd>{formatDateTime(t.created_at, timeZone) || "—"}</dd>
      <dt>Начата</dt>
      <dd>{formatDateTime(t.started_at, timeZone) || "—"}</dd>
      <dt>Завершена</dt>
      <dd>{formatDateTime(t.completed_at, timeZone) || "—"}</dd>
    </dl>
  );
}
