import { useCallback, useMemo, useState } from "react";
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { useMutation, useQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { Link, useParams, useSearchParams } from "react-router";
import { api } from "../api";
import type { ProjectListItem, ProjectMemberView, TaskListItem } from "../api/client";
import { errorText } from "../api/errors";
import { useMe } from "../auth/hooks";
import { formatDate } from "../lib/format";
import { statusLabel } from "../lib/status";
import { listQuery, myTasksQueryKey, projectsQueryKey } from "../projects/logic";
import { BoardCard, CardBody } from "./BoardCard";
import { boardQueryKey, groupByStatus } from "./cards";
import { BoardFilters } from "./BoardFilters";
import { filterFromParams, paramsFromFilter, tasksQuery, type BoardFilter, type TasksQuery } from "./filters";
import {
  BOARD_COLUMNS,
  canTransition,
  commentRequirement,
  needsNewAssignee,
  type Actor,
  type BoardStatus,
  type Status,
} from "./transitions";
import { TransitionDialog } from "./TransitionDialog";

/** Все задачи по запросу: сервер отдаёт до 200 за раз, остальное — по next_cursor. */
async function fetchAllTasks(key: string, query: TasksQuery, signal: AbortSignal): Promise<TaskListItem[]> {
  const items: TaskListItem[] = [];
  let cursor: string | undefined;
  do {
    const page = (await api.api.v1ProjectsTasksList(key, { ...query, cursor }, { signal })).data;
    items.push(...(page.items ?? []));
    cursor = page.next_cursor || undefined;
  } while (cursor);
  return items;
}

interface Move {
  task: TaskListItem;
  to: Status;
  comment?: string;
  assigneeId?: string;
  queryKey: QueryKey;
}

interface DialogState {
  task: TaskListItem;
  to: Status;
  comment: "rework" | "cancel" | null;
  chooseAssignee: boolean;
}

function Column({
  status,
  count,
  state,
  children,
}: {
  status: BoardStatus;
  count: number;
  state: "allowed" | "forbidden" | "source" | null;
  children: React.ReactNode;
}) {
  // В запрещённую колонку бросить нельзя: dnd-kit её не видит как цель.
  const { setNodeRef, isOver } = useDroppable({ id: status, disabled: state === "forbidden" });
  const cls = ["board-column", state ? `drop-${state}` : "", isOver && state === "allowed" ? "over" : ""]
    .filter(Boolean)
    .join(" ");
  return (
    <section ref={setNodeRef} className={cls} aria-label={statusLabel(status)}>
      <h3>
        {statusLabel(status)} <span className="muted">{count}</span>
      </h3>
      {state === "forbidden" && <p className="drop-hint">Сюда нельзя</p>}
      <div className="board-cards">{children}</div>
    </section>
  );
}

/** Доска проекта (ТЗ §7.3): задачи активного спринта по колонкам статусов. */
export function BoardPage() {
  const { key = "" } = useParams();
  const me = useMe();
  const queryClient = useQueryClient();
  const [params, setParams] = useSearchParams();
  const filter = useMemo(() => filterFromParams(params), [params]);
  const setFilter = useCallback(
    (f: BoardFilter, replace = false) => setParams(paramsFromFilter(f), { replace }),
    [setParams],
  );

  const project = useQuery({
    queryKey: ["project", key],
    queryFn: async ({ signal }) => (await api.api.v1ProjectsDetail(key, { signal })).data,
  });
  // Активный спринт — из карточки проекта в общем списке (тот же кэш, что на главной).
  const listFilter = project.data?.is_archived ? "archived" : "active";
  const projects = useQuery({
    queryKey: [...projectsQueryKey, listFilter],
    queryFn: async ({ signal }): Promise<ProjectListItem[]> =>
      (await api.api.v1ProjectsList(listQuery(listFilter), { signal })).data ?? [],
    enabled: project.isSuccess,
  });
  const members = useQuery({
    queryKey: ["project-members", key],
    queryFn: async ({ signal }): Promise<ProjectMemberView[]> =>
      (await api.api.v1ProjectsMembersList(key, { signal })).data ?? [],
  });

  const sprint = projects.data?.find((p) => p.key === key)?.active_sprint;
  const meId = me.data?.id;
  const query = sprint?.id ? tasksQuery(filter, sprint.id, meId) : null;
  const tasksKey: QueryKey = [...boardQueryKey(key), query];
  const tasks = useQuery({
    queryKey: tasksKey,
    queryFn: ({ signal }) => fetchAllTasks(key, query!, signal),
    // «Только мои» ждёт профиль: без id пользователя запрос вернул бы всех.
    enabled: query !== null && (!filter.mine || !!meId),
  });

  const memberById = useMemo(() => new Map((members.data ?? []).map((m) => [m.user_id, m])), [members.data]);
  const archived = project.data?.is_archived === true;
  const actor: Actor | null =
    me.data && project.data
      ? {
          userId: me.data.id ?? "",
          // Администратор системы действует с правами менеджера (§2).
          manager: project.data.my_role === "manager" || me.data.is_system_admin === true,
          member: !!project.data.my_role,
        }
      : null;

  const [actionError, setActionError] = useState("");
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [dragging, setDragging] = useState<TaskListItem | null>(null);

  const refresh = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: boardQueryKey(key) }),
      queryClient.invalidateQueries({ queryKey: projectsQueryKey }),
      queryClient.invalidateQueries({ queryKey: myTasksQueryKey }),
    ]);

  const transition = useMutation({
    mutationFn: (m: Move) =>
      api.api.v1TasksTransitionCreate(m.task.key!, { to: m.to, comment: m.comment, assignee_id: m.assigneeId }),
    // Карточка сразу встаёт в новую колонку; при ошибке возвращается обратно.
    onMutate: async (m) => {
      await queryClient.cancelQueries({ queryKey: m.queryKey });
      const prev = queryClient.getQueryData<TaskListItem[]>(m.queryKey);
      queryClient.setQueryData<TaskListItem[]>(m.queryKey, (old) =>
        old?.map((t) => (t.id === m.task.id ? { ...t, status: m.to } : t)),
      );
      return { prev };
    },
    onError: (e, m, ctx) => {
      if (ctx?.prev) queryClient.setQueryData(m.queryKey, ctx.prev);
      // Ошибку перехода из окна показывает само окно.
      if (!dialog) setActionError(`${m.task.key}: ${errorText(e)}`);
    },
    onSuccess: () => setDialog(null),
    onSettled: refresh,
  });

  const take = useMutation({
    mutationFn: (t: TaskListItem) => api.api.v1TasksTakeCreate(t.key!),
    // 409 TASK_ALREADY_TAKEN: сервер пишет, кто уже взял задачу.
    onError: (e, t) => setActionError(`${t.key}: ${errorText(e)}`),
    onSettled: refresh,
  });

  const allowed = (t: TaskListItem, to: Status) =>
    !!actor && !archived && canTransition({ status: t.status as Status, assigneeId: t.assignee_id, reporterId: t.reporter_id }, to, actor);
  const canDrag = (t: TaskListItem) => BOARD_COLUMNS.some((c) => c !== t.status && allowed(t, c));

  const startTransition = (t: TaskListItem, to: Status) => {
    setActionError("");
    const from = t.status as Status;
    const comment = commentRequirement(from, to);
    const assigneeActive = t.assignee_id ? memberById.get(t.assignee_id)?.is_active : undefined;
    const chooseAssignee = needsNewAssignee(from, to, assigneeActive);
    if (comment || chooseAssignee) {
      transition.reset();
      setDialog({ task: t, to, comment, chooseAssignee });
      return;
    }
    transition.mutate({ task: t, to, queryKey: tasksKey });
  };

  const sensors = useSensors(
    // Небольшой порог: щелчок по кнопке на карточке не начинает перетаскивание.
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );
  const onDragStart = (e: DragStartEvent) => {
    setActionError("");
    setDragging((e.active.data.current?.task as TaskListItem | undefined) ?? null);
  };
  const onDragEnd = (e: DragEndEvent) => {
    const t = e.active.data.current?.task as TaskListItem | undefined;
    setDragging(null);
    const to = e.over?.id as BoardStatus | undefined;
    if (!t || !to || to === t.status || !allowed(t, to)) return;
    startTransition(t, to);
  };

  const columnState = (status: BoardStatus) => {
    if (!dragging) return null;
    if (status === dragging.status) return "source";
    return allowed(dragging, status) ? "allowed" : "forbidden";
  };

  const byStatus = useMemo(() => groupByStatus(tasks.data ?? []), [tasks.data]);

  const loadError = project.error ?? projects.error ?? members.error ?? tasks.error;
  const loading = project.isPending || (project.isSuccess && projects.isPending);
  const dates = sprint ? [formatDate(sprint.start_date), formatDate(sprint.end_date)].filter(Boolean).join(" — ") : "";

  return (
    <section className="board-page">
      <div className="block-head">
        <h2>
          <span className="card-key">{key}</span> {project.data?.name}
        </h2>
        {archived && <span className="badge">Архив</span>}
        <nav className="tabs" aria-label="Разделы проекта">
          <span className="tab active">Доска</span>
          <Link className="tab" to={`/projects/${key}/backlog`}>
            Бэклог
          </Link>
        </nav>
        {sprint && (
          <span className="muted sprint-info">
            {sprint.name}
            {dates && `: ${dates}`}
          </span>
        )}
      </div>

      {loadError && <p role="alert">{errorText(loadError)}</p>}
      {loading && !loadError && <p className="muted">Загрузка…</p>}

      {projects.isSuccess && !sprint && (
        <div className="board-empty">
          <p>Нет активного спринта</p>
          <Link to={`/projects/${key}/backlog`}>Перейти в бэклог</Link>
        </div>
      )}

      {sprint && (
        <>
          <BoardFilters filter={filter} onChange={setFilter} members={members.data ?? []} />
          {actionError && (
            <p role="alert" className="board-error">
              {actionError}{" "}
              <button type="button" className="link" onClick={() => setActionError("")}>
                Скрыть
              </button>
            </p>
          )}
          {tasks.isPending && tasks.fetchStatus !== "idle" && <p className="muted">Загрузка задач…</p>}
          <DndContext
            sensors={sensors}
            onDragStart={onDragStart}
            onDragEnd={onDragEnd}
            onDragCancel={() => setDragging(null)}
          >
            <div className="board">
              {BOARD_COLUMNS.map((status) => {
                const list = byStatus.get(status) ?? [];
                return (
                  <Column key={status} status={status} count={list.length} state={columnState(status)}>
                    {list.map((t) => {
                      const free = status === "todo" && !t.assignee_id && !archived && !!actor?.member;
                      return (
                        <BoardCard
                          key={t.id}
                          task={t}
                          assignee={t.assignee_id ? memberById.get(t.assignee_id) : undefined}
                          draggable={canDrag(t)}
                          onTake={
                            free
                              ? () => {
                                  setActionError("");
                                  take.mutate(t);
                                }
                              : undefined
                          }
                          taking={take.isPending && take.variables?.id === t.id}
                        />
                      );
                    })}
                  </Column>
                );
              })}
            </div>
            <DragOverlay>
              {dragging && (
                <article className="task-card overlay">
                  <CardBody
                    task={dragging}
                    assignee={dragging.assignee_id ? memberById.get(dragging.assignee_id) : undefined}
                  />
                </article>
              )}
            </DragOverlay>
          </DndContext>
        </>
      )}

      {dialog && (
        <TransitionDialog
          task={dialog.task}
          to={dialog.to}
          comment={dialog.comment}
          chooseAssignee={dialog.chooseAssignee}
          activeMembers={(members.data ?? []).filter((m) => m.is_active !== false)}
          pending={transition.isPending}
          error={transition.isError ? errorText(transition.error) : ""}
          onCancel={() => setDialog(null)}
          onSubmit={(comment, assigneeId) =>
            transition.mutate({ task: dialog.task, to: dialog.to, comment, assigneeId, queryKey: tasksKey })
          }
        />
      )}
    </section>
  );
}
