import type { ProjectListItem } from "../api/client";
import { formatDate } from "../lib/format";

interface Props {
  project: ProjectListItem;
  /** Открыть проект; у удалённого проекта не задаётся — открывать нечего. */
  onOpen?: () => void;
  /** Кнопка «Восстановить» (удалённые проекты, администратор). */
  onRestore?: () => void;
  restoring?: boolean;
}

/** Карточка проекта (ТЗ §7.2): ключ, название, активный спринт с датами, открытые задачи и задачи на мне. */
export function ProjectCard({ project: p, onOpen, onRestore, restoring }: Props) {
  const sprint = p.active_sprint;
  const dates = sprint ? [formatDate(sprint.start_date), formatDate(sprint.end_date)].filter(Boolean).join(" — ") : "";
  return (
    <article
      className={onOpen ? "card clickable" : "card"}
      onClick={onOpen}
      onKeyDown={
        onOpen
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onOpen();
              }
            }
          : undefined
      }
      role={onOpen ? "link" : undefined}
      tabIndex={onOpen ? 0 : undefined}
    >
      <div className="card-head">
        <span className="card-key">{p.key}</span>
        {p.is_archived && <span className="badge">Архив</span>}
      </div>
      <h3>{p.name}</h3>
      <p className="muted">
        {sprint ? (
          <>
            {sprint.name}
            {dates && `: ${dates}`}
          </>
        ) : (
          "Нет активного спринта"
        )}
      </p>
      <dl className="card-stats">
        <div>
          <dt>Открытых задач</dt>
          <dd>{p.open_tasks ?? 0}</dd>
        </div>
        <div>
          <dt>На мне</dt>
          <dd>{p.my_open_tasks ?? 0}</dd>
        </div>
      </dl>
      {onRestore && (
        <button type="button" onClick={onRestore} disabled={restoring}>
          {restoring ? "Восстанавливаем…" : "Восстановить"}
        </button>
      )}
    </article>
  );
}
