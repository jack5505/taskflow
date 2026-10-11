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
import { keepPreviousData, useMutation, useQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { Link, useParams, useSearchParams } from "react-router";
import { api } from "../api";
import type { TaskListItem } from "../api/client";
import { errorText } from "../api/errors";
import { useMe } from "../auth/hooks";
import { formatDate } from "../lib/format";
import { statusLabel } from "../lib/status";
import { actorFor, fetchAllTasks, useActiveSprint, useProject, useProjectMembers } from "../projects/hooks";
import { myTasksQueryKey, projectsQueryKey } from "../projects/logic";
import { BoardCard, CardBody } from "./BoardCard";
import { applyMove, boardQueryKey, groupByStatus } from "./cards";
import { BoardFilters } from "./BoardFilters";
import { filterFromParams, paramsFromFilter, tasksQuery, type BoardFilter } from "./filters";
import {
  BOARD_COLUMNS,
  canTransition,
  transitionPrompt,
  type BoardStatus,
  type Status,
  type TransitionPrompt,
} from "./transitions";
import { TransitionDialog } from "./TransitionDialog";

const moveKey = ["board-move"];

interface Move {
  task: TaskListItem;
  to: Status;
  comment?: string;
  assigneeId?: string;
  queryKey: QueryKey;
}

interface DialogState extends TransitionPrompt {
  task: TaskListItem;
  to: Status;
}

/** Подсказки и объявления dnd-kit для экранного диктора — на русском (§10.3). */
const dndAccessibility = {
  screenReaderInstructions: {
    draggable:
      "Чтобы взять карточку, нажмите пробел или Enter. Стрелками переместите её в другую колонку, " +
      "пробелом или Enter положите, Escape — отмена.",
  },
  announcements: {
    onDragStart: () => "Карточка взята.",
    onDragOver: ({ over }: { over: { id: string | number } | null }) =>
      over ? `Над колонкой «${statusLabel(String(over.id))}».` : "Вне колонок.",
    onDragEnd: ({ over }: { over: { id: string | number } | null }) =>
      over ? `Карточка перенесена в колонку «${statusLabel(String(over.id))}».` : "Карточка возвращена на место.",
    onDragCancel: () => "Перетаскивание отменено.",
  },
};

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

  const project = useProject(key);
  const { query: projects, sprint } = useActiveSprint(key, project.data);
  const members = useProjectMembers(key);

  const meId = me.data?.id;
  const query = sprint?.id ? tasksQuery(filter, sprint.id, meId) : null;
  const tasksKey: QueryKey = [...boardQueryKey(key), query];
  const tasks = useQuery({
    queryKey: tasksKey,
    queryFn: ({ signal }) => fetchAllTasks(key, query!, signal),
    // «Только мои» ждёт профиль: без id пользователя запрос вернул бы всех.
    enabled: query !== null && (!filter.mine || !!meId),
    // При смене фильтра доска не мигает пустыми колонками, пока идёт новый запрос.
    placeholderData: keepPreviousData,
  });

  const memberById = useMemo(() => new Map((members.data ?? []).map((m) => [m.user_id, m])), [members.data]);
  const archived = project.data?.is_archived === true;
  const actor = actorFor(me.data, project.data);

  const [actionError, setActionError] = useState("");
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [dragging, setDragging] = useState<TaskListItem | null>(null);

  // Счётчики проектов и «Мои задачи» показывает главная: помечаем устаревшими, перезапросит она.
  // Доску перечитываем, когда закончились все переходы, иначе ответ на первый откатил бы второй.
  // own — вызвано из onSettled перехода: сам он в этот момент ещё числится выполняющимся.
  const refresh = (own = 0) => {
    if (queryClient.isMutating({ mutationKey: moveKey }) > own) return;
    void queryClient.invalidateQueries({ queryKey: boardQueryKey(key) });
    void queryClient.invalidateQueries({ queryKey: projectsQueryKey, refetchType: "none" });
    void queryClient.invalidateQueries({ queryKey: myTasksQueryKey, refetchType: "none" });
  };

  // Карточка сразу встаёт в новую колонку; при ошибке возвращается только она — по своему
  // исходному виду, а не по снимку всей доски (тот мог устареть из-за соседнего перехода).
  const moveOptions = {
    mutationKey: moveKey,
    mutationFn: (m: Move) =>
      api.api.v1TasksTransitionCreate(m.task.key!, { to: m.to, comment: m.comment, assignee_id: m.assigneeId }),
    onMutate: async (m: Move) => {
      await queryClient.cancelQueries({ queryKey: m.queryKey });
      queryClient.setQueryData<TaskListItem[]>(m.queryKey, (old) =>
        old?.map((t) => (t.id === m.task.id ? applyMove(t, m.to, meId ?? "") : t)),
      );
    },
    rollback: (m: Move) =>
      queryClient.setQueryData<TaskListItem[]>(m.queryKey, (old) =>
        old?.map((t) => (t.id === m.task.id ? m.task : t)),
      ),
  };
  // Перетаскивание без окна: ошибка — над доской.
  const move = useMutation({
    ...moveOptions,
    onError: (e, m) => {
      moveOptions.rollback(m);
      setActionError(`${m.task.key}: ${errorText(e)}`);
    },
    onSettled: () => refresh(1),
  });
  // Переход из окна: ошибка — в окне, успех закрывает окно.
  const dialogMove = useMutation({
    ...moveOptions,
    onError: (_e, m) => moveOptions.rollback(m),
    onSuccess: () => setDialog(null),
    onSettled: () => refresh(1),
  });

  const take = useMutation({
    mutationFn: (t: TaskListItem) => api.api.v1TasksTakeCreate(t.key!),
    // 409 TASK_ALREADY_TAKEN: сервер пишет, кто уже взял задачу.
    onError: (e, t) => setActionError(`${t.key}: ${errorText(e)}`),
    onSettled: () => refresh(),
  });

  const allowed = (t: TaskListItem, to: Status) =>
    !!actor && !archived && canTransition({ status: t.status as Status, assigneeId: t.assignee_id, reporterId: t.reporter_id }, to, actor);
  const canDrag = (t: TaskListItem) => BOARD_COLUMNS.some((c) => c !== t.status && allowed(t, c));

  const startTransition = (t: TaskListItem, to: Status) => {
    setActionError("");
    const assigneeActive = t.assignee_id ? memberById.get(t.assignee_id)?.is_active : undefined;
    const prompt = transitionPrompt(t.status as Status, to, assigneeActive);
    if (prompt) {
      dialogMove.reset();
      setDialog({ task: t, to, ...prompt });
      return;
    }
    move.mutate({ task: t, to, queryKey: tasksKey });
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
            accessibility={dndAccessibility}
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
          pending={dialogMove.isPending}
          error={dialogMove.isError ? errorText(dialogMove.error) : ""}
          onCancel={() => setDialog(null)}
          onSubmit={(comment, assigneeId) =>
            dialogMove.mutate({ task: dialog.task, to: dialog.to, comment, assigneeId, queryKey: tasksKey })
          }
        />
      )}
    </section>
  );
}
