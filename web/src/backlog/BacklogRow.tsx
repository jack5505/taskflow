import { useDraggable, useDroppable } from "@dnd-kit/core";
import { Link } from "react-router";
import type { ProjectMemberView, TaskListItem } from "../api/client";
import { formatDuration } from "../lib/duration";
import { statusLabel } from "../lib/status";

/** Ячейки строки задачи (§7.4): ключ, название, статус, исполнитель, оценка. */
export function RowCells({ task: t, assignee, linked }: { task: TaskListItem; assignee?: ProjectMemberView; linked: boolean }) {
  return (
    <>
      <span className="card-key">{t.key}</span>
      <span className="row-title">
        {/* Enter на ссылке не доходит до клавиатурного перетаскивания строки. */}
        {linked ? (
          <Link to={`/tasks/${t.key}`} onKeyDown={(e) => e.stopPropagation()}>
            {t.title}
          </Link>
        ) : (
          t.title
        )}
      </span>
      <span className={`muted status-${t.status}`}>{statusLabel(t.status)}</span>
      <span className="muted">
        {t.assignee_id ? assignee?.display_name || assignee?.email || "Исполнитель" : "Без исполнителя"}
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
      {...(props.draggable
        ? { ...drag.attributes, ...drag.listeners, "aria-roledescription": "перетаскиваемая задача" }
        : {})}
    >
      <RowCells task={props.task} assignee={props.assignee} linked />
    </li>
  );
}
