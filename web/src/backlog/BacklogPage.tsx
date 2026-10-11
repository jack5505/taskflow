import { useMemo, useState, type ReactNode } from "react";
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  pointerWithin,
  rectIntersection,
  useDroppable,
  useSensor,
  useSensors,
  type CollisionDetection,
  type DragEndEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { api } from "../api";
import type { SprintView, TaskListItem } from "../api/client";
import { errorText } from "../api/errors";
import { useMe, useTimeZone } from "../auth/hooks";
import { boardQueryKey } from "../board/cards";
import { PAGE_LIMIT } from "../board/filters";
import { formatDuration } from "../lib/duration";
import { formatDate } from "../lib/format";
import { actorFor, fetchAllTasks, useProject, useProjectMembers } from "../projects/hooks";
import { myTasksQueryKey, projectsQueryKey } from "../projects/logic";
import { BacklogRow, RowCells } from "./BacklogRow";
import {
  addDays,
  applyMove,
  BACKLOG,
  backlogQueryKey,
  buildSections,
  closeSummary,
  dropTarget,
  openSprints,
  sectionOf,
  sprintErrorText,
  sprintsQueryKey,
  today,
  totals,
  type DropOver,
  type MoveTarget,
  type Section,
} from "./logic";
import { CloseSprintDialog, CreateSprintDialog, StartSprintDialog, type SprintFields } from "./SprintDialogs";
import { useSprints } from "./useSprints";

const moveKey = ["backlog-move"];

/** Строки важнее секций: секция — цель, только когда указатель не над строкой (её конец, пустая). */
const collision: CollisionDetection = (args) => {
  const hits = pointerWithin(args);
  const list = hits.length ? hits : rectIntersection(args);
  return [...list].sort((a, b) => Number(String(b.id).startsWith("row:")) - Number(String(a.id).startsWith("row:")));
};

/** Подсказки dnd-kit для экранного диктора — на русском (§10.3). */
const dndAccessibility = {
  screenReaderInstructions: {
    draggable:
      "Чтобы взять задачу, нажмите пробел или Enter. Стрелками переместите её, пробелом или Enter " +
      "положите, Escape — отмена.",
  },
  announcements: {
    onDragStart: () => "Задача взята.",
    onDragOver: ({ over }: { over: { id: string | number } | null }) => (over ? "Над новым местом." : "Вне списка."),
    onDragEnd: ({ over }: { over: { id: string | number } | null }) =>
      over ? "Задача перенесена." : "Задача возвращена на место.",
    onDragCancel: () => "Перетаскивание отменено.",
  },
};

interface Move {
  task: TaskListItem;
  target: MoveTarget;
}

type Dialog = { kind: "create" } | { kind: "start"; sprint: SprintView } | { kind: "close"; sprint: SprintView };

function SectionBox({
  section,
  disabled,
  head,
  children,
}: {
  section: Section;
  disabled: boolean;
  head: ReactNode;
  children: ReactNode;
}) {
  const { setNodeRef, isOver } = useDroppable({
    id: `section:${section.id}`,
    data: { over: { kind: "section", sectionId: section.id } },
    disabled,
  });
  const name = section.sprint ? section.sprint.name : "Бэклог";
  return (
    <section ref={setNodeRef} className={`backlog-section${isOver ? " over" : ""}`} aria-label={name}>
      {head}
      {children}
    </section>
  );
}

function Totals({ tasks }: { tasks: TaskListItem[] }) {
  const t = totals(tasks);
  return (
    <span className="muted section-totals">
      Задач: {t.count}, оценка {formatDuration(t.estimate)}
    </span>
  );
}

/** Страница «Бэклог и спринты» (ТЗ §7.4): активный спринт, запланированные, бэклог. */
export function BacklogPage() {
  const { key = "" } = useParams();
  const me = useMe();
  const tz = useTimeZone();
  const queryClient = useQueryClient();
  const project = useProject(key);
  const members = useProjectMembers(key);
  const sprints = useSprints(key);

  const open = useMemo(() => openSprints(sprints.data ?? []), [sprints.data]);
  // Задачи всех секций одним запросом: незакрытые спринты и бэклог (null).
  const sprintIds = [...open.map((s) => s.id!), "null"];
  const tasksKey = [...backlogQueryKey(key), sprintIds];
  const tasks = useQuery({
    queryKey: tasksKey,
    queryFn: ({ signal }) => fetchAllTasks(key, { sprint_id: sprintIds, limit: PAGE_LIMIT }, signal),
    enabled: sprints.isSuccess,
    placeholderData: keepPreviousData,
  });
  const sections = useMemo(() => buildSections(sprints.data ?? [], tasks.data ?? []), [sprints.data, tasks.data]);

  const memberById = useMemo(() => new Map((members.data ?? []).map((m) => [m.user_id, m])), [members.data]);
  const archived = project.data?.is_archived === true;
  const actor = actorFor(me.data, project.data);
  const manager = !!actor?.manager && !archived;
  const canReorder = !!actor?.member && !archived;
  const active = open.find((s) => s.state === "active");
  const planned = open.filter((s) => s.state === "planned");

  const [notice, setNotice] = useState("");
  const [actionError, setActionError] = useState("");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [dragging, setDragging] = useState<TaskListItem | null>(null);

  // Бэклог и доска (общий ключ), спринты; счётчики главной — пометить устаревшими.
  const refresh = (own = 0) => {
    if (queryClient.isMutating({ mutationKey: moveKey }) > own) return;
    void queryClient.invalidateQueries({ queryKey: boardQueryKey(key) });
    void queryClient.invalidateQueries({ queryKey: sprintsQueryKey(key) });
    void queryClient.invalidateQueries({ queryKey: projectsQueryKey, refetchType: "none" });
    void queryClient.invalidateQueries({ queryKey: myTasksQueryKey, refetchType: "none" });
  };

  // Задача сразу встаёт на новое место; при ошибке список перечитывается с сервера.
  const move = useMutation({
    mutationKey: moveKey,
    mutationFn: ({ task, target }: Move) =>
      api.api.v1TasksMoveCreate(task.key!, {
        // Без sprint_id — та же секция: порядок может менять любой участник.
        ...(target.crossSection ? { sprint_id: target.sectionId === BACKLOG ? null : target.sectionId } : {}),
        after_task_id: target.afterTaskId,
      }),
    onMutate: async ({ task, target }: Move) => {
      await queryClient.cancelQueries({ queryKey: tasksKey });
      queryClient.setQueryData<TaskListItem[]>(tasksKey, (old) => old && applyMove(old, task.id!, target));
    },
    onError: (e, m) => setActionError(`${m.task.key}: ${errorText(e)}`),
    onSettled: () => refresh(1),
  });

  const create = useMutation({
    mutationFn: (f: SprintFields) =>
      api.api.v1ProjectsSprintsCreate(key, {
        name: f.name.trim() || undefined,
        goal: f.goal.trim() || undefined,
        start_date: f.start || undefined,
        end_date: f.end || undefined,
      }),
    onSuccess: () => setDialog(null),
    onSettled: () => refresh(),
  });

  // Запуск: сначала цель и даты из окна, затем запуск — сервер проверяет условия §5.2.
  const start = useMutation({
    mutationFn: async ({ sprint, f }: { sprint: SprintView; f: SprintFields }) => {
      await api.api.v1SprintsPartialUpdate(sprint.id!, {
        goal: f.goal.trim() || null,
        start_date: f.start,
        end_date: f.end,
      });
      return (await api.api.v1SprintsStartCreate(sprint.id!)).data;
    },
    onSuccess: (r) => {
      setDialog(null);
      const s = r.scope;
      setNotice(
        `Спринт «${r.sprint?.name}» запущен: задач ${s?.tasks ?? 0}, оценка ${formatDuration(s?.estimate_minutes ?? 0)}` +
          (s?.without_estimate ? `, без оценки: ${s.without_estimate}` : ""),
      );
    },
    onSettled: () => refresh(),
  });

  const close = useMutation({
    mutationFn: async ({ sprint, moveTo }: { sprint: SprintView; moveTo: string }) =>
      (await api.api.v1SprintsCloseCreate(sprint.id!, { move_open_to: moveTo })).data,
    onSuccess: (r) => {
      setDialog(null);
      const s = r.summary;
      setNotice(
        `Спринт «${r.sprint?.name}» завершён: готово ${s?.done ?? 0} из ${s?.total ?? 0}` +
          (s?.moved ? `, перенесено ${s.moved}` : ""),
      );
    },
    onSettled: () => refresh(),
  });

  const remove = useMutation({
    mutationFn: (sprint: SprintView) => api.api.v1SprintsDelete(sprint.id!),
    onError: (e, s) => setActionError(`«${s.name}»: ${errorText(e)}`),
    onSettled: () => refresh(),
  });

  const openDialog = (d: Dialog) => {
    setNotice("");
    setActionError("");
    create.reset();
    start.reset();
    close.reset();
    setDialog(d);
  };

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );
  const fromSection = dragging ? sectionOf(dragging) : null;
  // Исполнитель меняет только порядок: чужие секции во время перетаскивания закрыты (§2).
  const dropDisabled = (sectionId: string) => !!fromSection && !manager && sectionId !== fromSection;

  const onDragStart = (e: DragStartEvent) => {
    setActionError("");
    setDragging((e.active.data.current?.task as TaskListItem | undefined) ?? null);
  };
  const onDragEnd = (e: DragEndEvent) => {
    const t = e.active.data.current?.task as TaskListItem | undefined;
    setDragging(null);
    const over = e.over?.data.current?.over as DropOver | undefined;
    if (!t || !over) return;
    const target = dropTarget(sections, t.id!, over);
    if (!target || (target.crossSection && !manager)) return;
    move.mutate({ task: t, target });
  };

  const sectionHead = (s: Section) => {
    const sp = s.sprint;
    if (!sp) {
      return (
        <div className="section-head">
          <h3>Бэклог</h3>
          <Totals tasks={s.tasks} />
        </div>
      );
    }
    const dates = [formatDate(sp.start_date), formatDate(sp.end_date)].filter(Boolean).join(" — ");
    return (
      <div className="section-head">
        <h3>{sp.name}</h3>
        {sp.state === "active" && <span className="badge">Активный</span>}
        {sp.overdue && <span className="badge overdue">Просрочен</span>}
        {dates && <span className="muted">{dates}</span>}
        <Totals tasks={s.tasks} />
        {manager && (
          <span className="section-actions">
            {sp.state === "active" && (
              <button type="button" onClick={() => openDialog({ kind: "close", sprint: sp })}>
                Завершить
              </button>
            )}
            {sp.state === "planned" && !active && (
              <button type="button" className="primary" onClick={() => openDialog({ kind: "start", sprint: sp })}>
                Запустить
              </button>
            )}
            {sp.state === "planned" && (
              <button
                type="button"
                className="danger"
                disabled={remove.isPending}
                onClick={() =>
                  window.confirm(`Удалить спринт «${sp.name}»? Его задачи вернутся в бэклог.`) && remove.mutate(sp)
                }
              >
                Удалить
              </button>
            )}
          </span>
        )}
        {sp.goal && <p className="section-goal">Цель: {sp.goal}</p>}
      </div>
    );
  };

  const dialogTasks = dialog && dialog.kind !== "create" ? (sections.find((s) => s.id === dialog.sprint.id)?.tasks ?? []) : [];
  const loadError = project.error ?? members.error ?? sprints.error ?? tasks.error;
  const startDay = today(tz);

  return (
    <section className="backlog-page">
      <div className="block-head">
        <h2>
          <span className="card-key">{key}</span> {project.data?.name}
        </h2>
        {archived && <span className="badge">Архив</span>}
        <nav className="tabs" aria-label="Разделы проекта">
          <Link className="tab" to={`/projects/${key}`}>
            Доска
          </Link>
          <span className="tab active">Бэклог</span>
        </nav>
        <Link to={`/projects/${key}/backlog/archive`}>Архив спринтов</Link>
        {manager && (
          <button type="button" className="primary" onClick={() => openDialog({ kind: "create" })}>
            Создать спринт
          </button>
        )}
      </div>

      {loadError && <p role="alert">{errorText(loadError)}</p>}
      {(sprints.isPending || tasks.isPending) && !loadError && <p className="muted">Загрузка…</p>}
      {notice && (
        <p className="notice">
          {notice}{" "}
          <button type="button" className="link" onClick={() => setNotice("")}>
            Скрыть
          </button>
        </p>
      )}
      {actionError && (
        <p role="alert" className="board-error">
          {actionError}{" "}
          <button type="button" className="link" onClick={() => setActionError("")}>
            Скрыть
          </button>
        </p>
      )}

      {tasks.isSuccess && (
        <DndContext
          sensors={sensors}
          collisionDetection={collision}
          accessibility={dndAccessibility}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDragCancel={() => setDragging(null)}
        >
          {sections.map((s) => (
            <SectionBox key={s.id} section={s} disabled={dropDisabled(s.id)} head={sectionHead(s)}>
              {s.tasks.length === 0 ? (
                <p className="muted section-empty">
                  {s.sprint && manager ? "Перетащите сюда задачи из бэклога" : "Задач нет"}
                </p>
              ) : (
                <ul className="backlog-rows">
                  {s.tasks.map((t) => (
                    <BacklogRow
                      key={t.id}
                      task={t}
                      assignee={t.assignee_id ? memberById.get(t.assignee_id) : undefined}
                      draggable={canReorder}
                      dropDisabled={dropDisabled(s.id)}
                    />
                  ))}
                </ul>
              )}
            </SectionBox>
          ))}
          <DragOverlay>
            {dragging && (
              <div className="backlog-row overlay">
                <RowCells
                  task={dragging}
                  assignee={dragging.assignee_id ? memberById.get(dragging.assignee_id) : undefined}
                  linked={false}
                />
              </div>
            )}
          </DragOverlay>
        </DndContext>
      )}

      {dialog?.kind === "create" && (
        <CreateSprintDialog
          pending={create.isPending}
          error={create.isError ? sprintErrorText(create.error) : ""}
          onEdit={() => create.isError && create.reset()}
          onCancel={() => setDialog(null)}
          onSubmit={(f) => create.mutate(f)}
        />
      )}
      {dialog?.kind === "start" && (
        <StartSprintDialog
          sprint={dialog.sprint}
          scope={totals(dialogTasks)}
          defaults={{ start: startDay, end: addDays(startDay, 14) }}
          pending={start.isPending}
          error={start.isError ? sprintErrorText(start.error) : ""}
          onEdit={() => start.isError && start.reset()}
          onCancel={() => setDialog(null)}
          onSubmit={(f) => start.mutate({ sprint: dialog.sprint, f })}
        />
      )}
      {dialog?.kind === "close" && (
        <CloseSprintDialog
          sprint={dialog.sprint}
          summary={closeSummary(dialogTasks)}
          planned={planned}
          pending={close.isPending}
          error={close.isError ? sprintErrorText(close.error) : ""}
          onEdit={() => close.isError && close.reset()}
          onCancel={() => setDialog(null)}
          onSubmit={(moveTo) => close.mutate({ sprint: dialog.sprint, moveTo })}
        />
      )}
    </section>
  );
}
