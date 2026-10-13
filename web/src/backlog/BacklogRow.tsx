import { useDraggable, useDroppable } from "@dnd-kit/core";
import { Link } from "react-router";
import type { ProjectMemberView, TaskListItem } from "../api/client";
import { formatDuration } from "../lib/duration";
import { statusLabel } from "../lib/status";
import { Avatar } from "../ui/Avatar";
import { TypeIcon } from "../ui/Icon";
import { splitDraggable } from "../ui/dnd";

/** Ячейки строки задачи (§7.4): ключ, название, статус, исполнитель, оценка. */
export function RowCells({
  task: t,
  assignee,
  linked,
  linkProps,
}: {
  task: TaskListItem;
  assignee?: ProjectMemberView;
  linked: boolean;
  linkProps?: Record<string, unknown>;
}) {
  return (
    <>
      <span className="row-key">
        <TypeIcon type={t.type} size={14} />
        <span className="card-key">{t.key}</span>
      </span>
      <span className="row-title">
        {/* Ссылка — единственный элемент строки в порядке Tab: Enter открывает, пробел тянет. */}
        {linked ? (
          <Link to={`/tasks/${t.key}`} {...linkProps}>
            {t.title}
          </Link>
        ) : (
          t.title
        )}
      </span>
      <span className={`muted status-${t.status}`}>{statusLabel(t.status)}</span>
      <span className="row-assignee muted">
        <Avatar id={t.assignee_id} name={assignee?.display_name || assignee?.email} size="sm" decorative />
        <span className="row-assignee-name">
          {t.assignee_id ? assignee?.display_name || assignee?.email || "Исполнитель" : "Без исполнителя"}
        </span>
      </span>
      <span className="muted nowrap" title="Оценка">
        {formatDuration(t.estimate_minutes) || "—"}
      </span>
    </>
  );
}

/**
 * Строка задачи: её можно тянуть (draggable) и на неё можно бросить другую (dropDisabled —
 * сюда нельзя: чужая секция для исполнителя).
 */
export function BacklogRow(props: {
  task: TaskListItem;
  assignee?: ProjectMemberView;
  draggable: boolean;
  dropDisabled: boolean;
}) {
  const id = props.task.id!;
  const drag = useDraggable({ id, data: { task: props.task }, disabled: !props.draggable });
  const drop = useDroppable({ id: `row:${id}`, data: { over: { kind: "row", taskId: id } }, disabled: props.dropDisabled });
  const dnd = props.draggable ? splitDraggable(drag.attributes, drag.listeners, "перетаскиваемая задача") : null;
  const cls = ["backlog-row", drag.isDragging ? "dragging" : "", drop.isOver ? "over" : "", props.draggable ? "draggable" : ""]
    .filter(Boolean)
    .join(" ");
  return (
    <li
      ref={(el) => {
        drag.setNodeRef(el);
        drop.setNodeRef(el);
      }}
      className={cls}
      {...(dnd ? dnd.wrapper : {})}
    >
      <RowCells task={props.task} assignee={props.assignee} linked linkProps={dnd?.link} />
    </li>
  );
}
