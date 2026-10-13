import { useState, type FormEvent } from "react";
import type { ProjectMemberView, TaskListItem } from "../api/client";
import { statusLabel } from "../lib/status";
import { commentError, type Status } from "./transitions";
import { Modal } from "../ui/Modal";

interface Props {
  task: Pick<TaskListItem, "key" | "title" | "status">;
  to: Status;
  /** Нужен комментарий: причина возврата на доработку или отмены. */
  comment: "rework" | "cancel" | null;
  /** Нужно выбрать нового исполнителя (переоткрытие, исполнитель деактивирован). */
  chooseAssignee: boolean;
  activeMembers: ProjectMemberView[];
  pending: boolean;
  /** Ошибка сервера по этому переходу. */
  error: string;
  onSubmit: (comment: string | undefined, assigneeId: string | undefined) => void;
  onCancel: () => void;
}

const titles = { rework: "Вернуть на доработку", cancel: "Отменить задачу" };

/** Модальное окно перехода, которому нужны данные: комментарий (§4) или исполнитель. */
export function TransitionDialog(p: Props) {
  const [comment, setComment] = useState("");
  const [assignee, setAssignee] = useState("");
  const [localError, setLocalError] = useState("");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    let err = p.comment ? commentError(p.comment, comment) : "";
    if (!err && p.chooseAssignee && !assignee) err = "Исполнитель деактивирован — выберите активного";
    setLocalError(err);
    if (!err) p.onSubmit(p.comment ? comment.trim() : undefined, p.chooseAssignee ? assignee : undefined);
  };

  const title = p.comment ? titles[p.comment] : "Переоткрыть задачу";
  const error = localError || p.error;
  return (
    <Modal labelledBy="transition-title" onCancel={p.onCancel}>
      <form
        className="form"
        onSubmit={submit}
      >
        <h2 id="transition-title">{title}</h2>
        <p className="muted">
          {p.task.key} «{p.task.title}»: {statusLabel(p.task.status)} → {statusLabel(p.to)}
        </p>
        {p.comment && (
          <label>
            {p.comment === "rework" ? "Причина возврата" : "Причина отмены"}
            <textarea
              autoFocus
              rows={4}
              maxLength={5000}
              value={comment}
              onChange={(e) => {
                setComment(e.target.value);
                setLocalError("");
              }}
            />
            {p.comment === "rework" && <span className="hint">Не короче 5 символов</span>}
          </label>
        )}
        {p.chooseAssignee && (
          <label>
            Новый исполнитель
            <select
              autoFocus={!p.comment}
              value={assignee}
              onChange={(e) => {
                setAssignee(e.target.value);
                setLocalError("");
              }}
            >
              <option value="">— выберите —</option>
              {p.activeMembers.map((m) => (
                <option key={m.user_id} value={m.user_id}>
                  {m.display_name || m.email}
                </option>
              ))}
            </select>
            <span className="hint">Прежний исполнитель деактивирован</span>
          </label>
        )}
        {error && <p role="alert">{error}</p>}
        <div className="actions">
          <button type="submit" className="primary" disabled={p.pending}>
            {p.pending ? "Сохраняем…" : "Подтвердить"}
          </button>
          <button type="button" onClick={p.onCancel}>
            Отмена
          </button>
        </div>
      </form>
    </Modal>
  );
}
