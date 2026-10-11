import { useDraggable } from "@dnd-kit/core";
import type { ProjectMemberView, TaskListItem } from "../api/client";
import { formatSpentEstimate } from "../lib/duration";
import { formatDate } from "../lib/format";
import { initials } from "./cards";
import { priorityLabels, typeLabels, type Priority, type TaskType } from "./filters";

const typeIcons: Record<TaskType, string> = { task: "✓", bug: "✱", feature: "★" };
const priorityIcons: Record<Priority, string> = { low: "↓", medium: "=", high: "↑", critical: "⇈" };

function Avatar({ assigneeId, member }: { assigneeId?: string; member?: ProjectMemberView }) {
  if (!assigneeId) return <span className="avatar empty" title="Без исполнителя" aria-label="Без исполнителя" />;
  const name = member?.display_name || member?.email || "Исполнитель";
  return (
    <span className="avatar" title={name} aria-label={`Исполнитель: ${name}`}>
      {initials(member?.display_name || member?.email) || "?"}
    </span>
  );
}

interface CardProps {
  task: TaskListItem;
  assignee?: ProjectMemberView;
}

/** Содержимое карточки доски (ТЗ §7.3). Индикатор таймера появится вместе с таймером. */
export function CardBody({ task: t, assignee }: CardProps) {
  const type = (t.type ?? "task") as TaskType;
  const priority = (t.priority ?? "medium") as Priority;
  const due = formatDate(t.due_date);
  return (
    <>
      <div className="task-head">
        <span className={`type-icon type-${type}`} title={typeLabels[type]} aria-label={typeLabels[type]}>
          {typeIcons[type]}
        </span>
        <span className="card-key">{t.key}</span>
        <span
          className={`priority-icon priority-${priority}`}
          title={`Приоритет: ${priorityLabels[priority]}`}
          aria-label={`Приоритет: ${priorityLabels[priority]}`}
        >
          {priorityIcons[priority]}
        </span>
      </div>
      <div className="task-title">{t.title}</div>
      <div className="task-foot">
        <Avatar assigneeId={t.assignee_id} member={assignee} />
        <span className="muted" title="Списано / оценка">
          {formatSpentEstimate(t.spent_minutes, t.estimate_minutes)}
        </span>
        {due && (
          <span className={t.overdue ? "due overdue" : "due muted"} title={t.overdue ? "Срок просрочен" : "Срок"}>
            {due}
          </span>
        )}
      </div>
    </>
  );
}

/**
 * Карточка, которую можно перетащить в другую колонку. Тянется за содержимое; кнопка
 * «Взять в работу» лежит вне перетаскиваемой области, чтобы не быть кнопкой внутри кнопки.
 */
export function BoardCard(
  props: CardProps & {
    draggable: boolean;
    /** Кнопка «Взять в работу»: свободная задача в «К выполнению». */
    onTake?: () => void;
    taking?: boolean;
  },
) {
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: props.task.id!,
    data: { task: props.task },
    disabled: !props.draggable,
  });
  return (
    <article ref={setNodeRef} className={`task-card${isDragging ? " dragging" : ""}`}>
      <div
        className={props.draggable ? "task-drag draggable" : "task-drag"}
        {...(props.draggable ? { ...attributes, ...listeners, "aria-roledescription": "перетаскиваемая карточка" } : {})}
      >
        <CardBody task={props.task} assignee={props.assignee} />
      </div>
      {props.onTake && (
        <button
          type="button"
          className="take"
          disabled={props.taking}
          onClick={props.onTake}
        >
          {props.taking ? "Берём…" : "Взять в работу"}
        </button>
      )}
    </article>
  );
}
