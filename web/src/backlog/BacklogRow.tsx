import { useDraggable, useDroppable } from "@dnd-kit/core";
import { Link } from "react-router";
import type { ProjectMemberView, TaskListItem } from "../api/client";
import { formatDuration } from "../lib/duration";
import { formatDateTime } from "../lib/format";
import { statusLabel } from "../lib/status";
import { Avatar } from "../ui/Avatar";
import { TypeIcon } from "../ui/Icon";
import { StatusDot } from "../ui/StatusDot";
import { splitDraggable } from "../ui/dnd";

/** Ячейки строки задачи (§7.4): ключ, название, статус, последнее изменение, исполнитель, оценка. */
export function RowCells({
  task: t,
  assignee,
  linked,
  linkProps,
  timeZone,
}: {
  task: TaskListItem;
  assignee?: ProjectMemberView;
  linked: boolean;
  linkProps?: Record<string, unknown>;
  timeZone?: string;
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
      {/* Значок статуса + подпись; для завершённой задачи — когда она попала в конечный статус. */}
      <span className="row-status">
        <span className="status-label muted">
          <StatusDot status={t.status} />
          {statusLabel(t.status)}
        </span>
        {t.completed_at && (
          <time className="row-done-at muted" dateTime={t.completed_at} title="Завершена">
            {formatDateTime(t.completed_at, timeZone)}
          </time>
        )}
      </span>
      {/* Последнее изменение задачи: любое поле, статус, перенос между секциями. */}
      <time className="row-updated muted" dateTime={t.updated_at} title="Последнее изменение">
        {formatDateTime(t.updated_at, timeZone)}
      </time>
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
  timeZone?: string;
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
      <RowCells task={props.task} assignee={props.assignee} linked linkProps={dnd?.link} timeZone={props.timeZone} />
    </li>
  );
}
