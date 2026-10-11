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
  /** Кнопка «Взять в работу»: свободная задача в «К выполнению». */
  onTake?: () => void;
  taking?: boolean;
}

/** Содержимое карточки доски (ТЗ §7.3). Индикатор таймера появится вместе с таймером. */
export function CardBody({ task: t, assignee, onTake, taking }: CardProps) {
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
      {onTake && (
        <button
          type="button"
          className="take"
          disabled={taking}
          // Нажатие на кнопку не должно начинать перетаскивание карточки.
          onPointerDown={(e) => e.stopPropagation()}
          onKeyDown={(e) => e.stopPropagation()}
          onClick={onTake}
        >
          {taking ? "Берём…" : "Взять в работу"}
        </button>
      )}
    </>
  );
}

/** Карточка, которую можно перетащить в другую колонку. */
export function BoardCard(props: CardProps & { draggable: boolean }) {
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: props.task.id!,
    data: { task: props.task },
    disabled: !props.draggable,
  });
  return (
    <article
      ref={setNodeRef}
      className={`task-card${props.draggable ? " draggable" : ""}${isDragging ? " dragging" : ""}`}
      aria-roledescription={props.draggable ? "перетаскиваемая карточка" : undefined}
      {...(props.draggable ? { ...attributes, ...listeners } : {})}
    >
      <CardBody {...props} />
    </article>
  );
}
