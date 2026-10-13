import type { ReactNode } from "react";
import { useDraggable } from "@dnd-kit/core";
import { Link } from "react-router";
import type { ProjectMemberView, TaskListItem } from "../api/client";
import { formatSpentEstimate } from "../lib/duration";
import { formatDate } from "../lib/format";
import { Avatar } from "../ui/Avatar";
import { Icon, PriorityIcon, TypeIcon } from "../ui/Icon";
import { splitDraggable } from "../ui/dnd";

interface CardProps {
  task: TaskListItem;
  assignee?: ProjectMemberView;
  /**
   * На задаче идёт таймер. API отдаёт только свой таймер (GET /me), поэтому индикатор — только
   * у своего: чужие таймеры в списке задач не приходят.
   */
  timerRunning?: boolean;
}

/** Содержимое карточки доски (ТЗ §7.3). */
export function CardBody({
  task: t,
  assignee,
  timerRunning,
  linked = false,
  linkProps,
}: CardProps & { linked?: boolean; linkProps?: Record<string, unknown> }) {
  const due = formatDate(t.due_date);
  return (
    <>
      <div className="task-title">
        {/* Карточка задачи (§7.5); в окне перетаскивания — просто текст. Ссылка — единственный
            элемент карточки в порядке Tab: Enter открывает задачу, пробел начинает перетаскивание. */}
        {linked ? (
          <Link to={`/tasks/${t.key}`} {...linkProps}>
            {t.title}
          </Link>
        ) : (
          t.title
        )}
      </div>
      <div className="task-foot">
        <TypeIcon type={t.type} />
        <span className="card-key">{t.key}</span>
        <PriorityIcon priority={t.priority} />
        {due && (
          <span className={t.overdue ? "due overdue" : "due muted"} title={t.overdue ? "Срок просрочен" : "Срок"}>
            {due}
          </span>
        )}
        <span className="task-foot-end">
          {timerRunning && (
            <span className="timer-indicator">
              <Icon name="timer" label="Идёт таймер" />
            </span>
          )}
          <span className="muted" title="Списано / оценка">
            {formatSpentEstimate(t.spent_minutes, t.estimate_minutes)}
          </span>
          <Avatar id={t.assignee_id} name={assignee?.display_name || assignee?.email} />
        </span>
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
    /** Кнопка «Старт / Стоп» таймера (§6.4) — тоже вне перетаскиваемой области. */
    timer?: ReactNode;
  },
) {
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: props.task.id!,
    data: { task: props.task },
    disabled: !props.draggable,
  });
  const dnd = props.draggable ? splitDraggable(attributes, listeners, "перетаскиваемая карточка") : null;
  return (
    <article ref={setNodeRef} className={`task-card${isDragging ? " dragging" : ""}`}>
      <div className={props.draggable ? "task-drag draggable" : "task-drag"} {...(dnd ? dnd.wrapper : {})}>
        <CardBody task={props.task} assignee={props.assignee} timerRunning={props.timerRunning} linked linkProps={dnd?.link} />
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
      {props.timer}
    </article>
  );
}
