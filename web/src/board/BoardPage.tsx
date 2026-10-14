import { useCallback, useMemo, useState } from "react";
import {
  DndContext,
  DragOverlay,
  useDroppable,
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
import { TimerButton } from "../timer/TimerButton";
import { canStartTimer } from "../timer/timer";
import { timerChanged } from "../timer/hooks";
import { BoardCard, CardBody } from "./BoardCard";
import { applyMove, boardQueryKey, groupByStatus, moveKey } from "./cards";
import { daysLeftText, sprintDaysLeft, today } from "../backlog/logic";
import { BoardFilters } from "./BoardFilters";
import { emptyFilter, filterFromParams, isFilterEmpty, paramsFromFilter, tasksQuery, type BoardFilter } from "./filters";
import {
  BOARD_COLUMNS,
  canTransition,
  transitionPrompt,
  type BoardStatus,
  type Status,
  type TransitionPrompt,
} from "./transitions";
import { TransitionDialog } from "./TransitionDialog";
import { ProjectHeader } from "../projects/ProjectHeader";
import { useToast } from "../ui/Toast";
import { LoadError } from "../ui/LoadError";
import { useDragSensors, useReducedMotion } from "../ui/dnd";


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
      "Enter открывает задачу. Чтобы взять карточку, нажмите пробел, стрелками переместите её в другую " +
      "колонку, пробелом или Enter положите, Escape — отмена.",
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
  loaded,
  state,
  children,
}: {
  status: BoardStatus;
  count: number;
  /** Задачи загружены: «Задач нет» — только тогда, а не во время загрузки или после ошибки. */
  loaded: boolean;
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
      <div className="board-cards">
        {count === 0 && loaded ? (
          <p className="column-empty">{state === "allowed" ? "Перетащите сюда" : "Задач нет"}</p>
        ) : (
          children
        )}
      </div>
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
  const timerKey = me.data?.timer?.task_key;

  const toast = useToast();
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

  // Переход из «В работе» останавливает таймер на задаче с записью времени (§6.4): шапка и
  // карточка этой задачи должны это увидеть.
  const settled = (m: Move) => {
    refresh(1);
    if (m.task.key && m.task.key === timerKey) timerChanged(queryClient, [m.task.key]);
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
      toast.error(`${m.task.key}: ${errorText(e)}`);
    },
    onSuccess: (_r, m) => toast.success(`${m.task.key}: ${statusLabel(m.to)}`),
    onSettled: (_r, _e, m) => settled(m),
  });
  // Переход из окна: ошибка — в окне, успех закрывает окно.
  const dialogMove = useMutation({
    ...moveOptions,
    onError: (_e, m) => moveOptions.rollback(m),
    onSuccess: (_r, m) => {
      setDialog(null);
      toast.success(`${m.task.key}: ${statusLabel(m.to)}`);
    },
    onSettled: (_r, _e, m) => settled(m),
  });

  const take = useMutation({
    mutationFn: (t: TaskListItem) => api.api.v1TasksTakeCreate(t.key!),
    // 409 TASK_ALREADY_TAKEN: сервер пишет, кто уже взял задачу.
    onError: (e, t) => toast.error(`${t.key}: ${errorText(e)}`),
    onSuccess: (_r, t) => toast.success(`${t.key} взята в работу`),
    onSettled: () => refresh(),
  });

  const allowed = (t: TaskListItem, to: Status) =>
    !!actor && !archived && canTransition({ status: t.status as Status, assigneeId: t.assignee_id, reporterId: t.reporter_id }, to, actor);
  const canDrag = (t: TaskListItem) => BOARD_COLUMNS.some((c) => c !== t.status && allowed(t, c));

  const startTransition = (t: TaskListItem, to: Status) => {
    const assigneeActive = t.assignee_id ? memberById.get(t.assignee_id)?.is_active : undefined;
    const prompt = transitionPrompt(t.status as Status, to, assigneeActive);
    if (prompt) {
      dialogMove.reset();
      setDialog({ task: t, to, ...prompt });
      return;
    }
    move.mutate({ task: t, to, queryKey: tasksKey });
  };

  const reducedMotion = useReducedMotion();
  const sensors = useDragSensors();
  const onDragStart = (e: DragStartEvent) => {
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
  // «Сегодня» и просрочка спринта — по поясу проекта (§5.3).
  const left = sprint ? sprintDaysLeft(sprint.end_date, today(project.data?.timezone)) : null;

  return (
    <section className="board-page">
      <ProjectHeader projectKey={key}>
        {sprint && (
          <span className="sprint-info">
            <strong>{sprint.name}</strong>
            {dates && <span className="muted">{dates}</span>}
            {left !== null &&
              (left > 0 ? (
                <span className="muted">{daysLeftText(left)}</span>
              ) : (
                <span className="badge overdue">{daysLeftText(left)}</span>
              ))}
          </span>
        )}
      </ProjectHeader>

      {loadError && (
        <LoadError
          error={loadError}
          onRetry={() => [project, projects, members, tasks].forEach((q) => q.isError && void q.refetch())}
        />
      )}
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
          {tasks.isPending && tasks.fetchStatus !== "idle" && <p className="muted">Загрузка задач…</p>}
          <DndContext
            sensors={sensors}
            accessibility={dndAccessibility}
            onDragStart={onDragStart}
            onDragEnd={onDragEnd}
            onDragCancel={() => setDragging(null)}
          >
            {tasks.isSuccess && (tasks.data ?? []).length === 0 && !isFilterEmpty(filter) && (
              <p className="board-nothing" role="status">
                Ничего не найдено по фильтрам.{" "}
                <button type="button" className="link" onClick={() => setFilter(emptyFilter)}>
                  Сбросить фильтры
                </button>
              </p>
            )}
            <div className="board">
              {BOARD_COLUMNS.map((status) => {
                const list = byStatus.get(status) ?? [];
                return (
                  <Column key={status} status={status} count={list.length} loaded={tasks.isSuccess} state={columnState(status)}>
                    {list.map((t) => {
                      const free = status === "todo" && !t.assignee_id && !archived && !!actor?.member;
                      const timerHere = !!timerKey && timerKey === t.key;
                      const canStart = canStartTimer(t, meId, archived);
                      return (
                        <BoardCard
                          key={t.id}
                          task={t}
                          assignee={t.assignee_id ? memberById.get(t.assignee_id) : undefined}
                          draggable={canDrag(t)}
                          onTake={
                            free
                              ? () => {
                                  take.mutate(t);
                                }
                              : undefined
                          }
                          taking={take.isPending && take.variables?.id === t.id}
                          timerRunning={timerHere}
                          // Всегда: кнопка сама решает, что показать, и держит итог «списано» после «Стоп».
                          timer={<TimerButton taskKey={t.key!} canStart={canStart} className="card-timer" />}
                        />
                      );
                    })}
                  </Column>
                );
              })}
            </div>
            <DragOverlay dropAnimation={reducedMotion ? null : undefined}>
              {dragging && (
                <article className="task-card overlay">
                  <CardBody
                    task={dragging}
                    assignee={dragging.assignee_id ? memberById.get(dragging.assignee_id) : undefined}
                    timerRunning={!!timerKey && timerKey === dragging.key}
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
