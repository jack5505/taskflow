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
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
  currentPlace,
  dropTarget,
  openSprints,
  sectionOf,
  sprintErrorText,
  sprintsQueryKey,
  startPatch,
  today,
  totals,
  type BacklogSnapshot,
  type DropOver,
  type MoveTarget,
  type Section,
} from "./logic";
import { CloseSprintDialog, CreateSprintDialog, StartSprintDialog, type SprintFields } from "./SprintDialogs";
import { ProjectHeader } from "../projects/ProjectHeader";
import { useConfirm } from "../ui/Confirm";
import { useToast } from "../ui/Toast";
import { LoadError } from "../ui/LoadError";
import { keyboardCodes, useReducedMotion } from "../ui/dnd";

const moveKey = ["backlog-move"];

/** Цель и даты сохранены, а запуск отклонён. */
class SavedButNotStarted extends Error {
  readonly reason: unknown;
  constructor(reason: unknown) {
    super("saved");
    this.reason = reason;
  }
}

function startErrorText(e: unknown): string {
  return e instanceof SavedButNotStarted
    ? `Цель и даты сохранены, но спринт не запущен: ${sprintErrorText(e.reason)}`
    : sprintErrorText(e);
}

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
      "Enter открывает задачу. Чтобы взять её, нажмите пробел, стрелками переместите, пробелом или Enter " +
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
  /** Прежнее место — для отката при ошибке. */
  undo: MoveTarget;
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
  const toast = useToast();
  const confirm = useConfirm();
  const { key = "" } = useParams();
  const me = useMe();
  const tz = useTimeZone();
  const queryClient = useQueryClient();
  const project = useProject(key);
  const members = useProjectMembers(key);
  // Спринты и задачи всех секций — одним снимком: секции не собираются из списка спринтов одной
  // версии и задач другой (после завершения спринта его задачи не пропадают на время запроса).
  const snapshot = useQuery({
    queryKey: backlogQueryKey(key),
    queryFn: async ({ signal }): Promise<BacklogSnapshot> => {
      const sprints = (await api.api.v1ProjectsSprintsList(key, { signal })).data ?? [];
      const ids = [...openSprints(sprints).map((s) => s.id!), "null"];
      return { sprints, tasks: await fetchAllTasks(key, { sprint_id: ids, limit: PAGE_LIMIT }, signal) };
    },
    enabled: key !== "",
  });
  const open = useMemo(() => openSprints(snapshot.data?.sprints ?? []), [snapshot.data]);
  const sections = useMemo(
    () => buildSections(snapshot.data?.sprints ?? [], snapshot.data?.tasks ?? []),
    [snapshot.data],
  );

  const memberById = useMemo(() => new Map((members.data ?? []).map((m) => [m.user_id, m])), [members.data]);
  const archived = project.data?.is_archived === true;
  const actor = actorFor(me.data, project.data);
  const manager = !!actor?.manager && !archived;
  // Порядок — любой участник; администратор системы — с правами менеджера и без роли в проекте.
  const canReorder = !!actor && (actor.member || actor.manager) && !archived;
  const active = open.find((s) => s.state === "active");
  const planned = open.filter((s) => s.state === "planned");

  const [notice, setNotice] = useState("");
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

  const moveInCache = (taskId: string, target: MoveTarget) =>
    queryClient.setQueryData<BacklogSnapshot>(backlogQueryKey(key), (old) =>
      old && { ...old, tasks: applyMove(old.tasks, taskId, target) },
    );

  // Задача сразу встаёт на новое место; при ошибке возвращается на прежнее — только она, а не
  // снимок всего списка (тот мог устареть из-за соседнего переноса).
  const move = useMutation({
    mutationKey: moveKey,
    mutationFn: ({ task, target }: Move) =>
      api.api.v1TasksMoveCreate(task.key!, {
        // Без sprint_id — та же секция: порядок может менять любой участник.
        ...(target.crossSection ? { sprint_id: target.sectionId === BACKLOG ? null : target.sectionId } : {}),
        after_task_id: target.afterTaskId,
      }),
    onMutate: async ({ task, target }: Move) => {
      await queryClient.cancelQueries({ queryKey: backlogQueryKey(key) });
      moveInCache(task.id!, target);
    },
    onError: (e, m) => {
      moveInCache(m.task.id!, m.undo);
      toast.error(`${m.task.key}: ${errorText(e)}`);
    },
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
    onSuccess: (r) => {
      setDialog(null);
      toast.success(`Спринт «${r.data.name}» создан`);
    },
    onSettled: () => refresh(),
  });

  // Запуск: сначала изменённые в окне цель и даты, затем запуск — сервер проверяет условия §5.2.
  // Если правка прошла, а запуск нет, окно говорит, что цель и даты уже сохранены.
  const start = useMutation({
    mutationFn: async ({ sprint, f }: { sprint: SprintView; f: SprintFields }) => {
      const patch = startPatch(sprint, f);
      if (patch) await api.api.v1SprintsPartialUpdate(sprint.id!, patch);
      try {
        return (await api.api.v1SprintsStartCreate(sprint.id!)).data;
      } catch (e) {
        throw patch ? new SavedButNotStarted(e) : e;
      }
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
    onSuccess: (_, s) => toast.success(`Спринт «${s.name}» удалён, его задачи — в бэклоге`),
    onError: (e, s) => toast.error(`«${s.name}»: ${errorText(e)}`),
    onSettled: () => refresh(),
  });

  const openDialog = (d: Dialog) => {
    setNotice("");
    create.reset();
    start.reset();
    close.reset();
    setDialog(d);
  };

  const reducedMotion = useReducedMotion();
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { keyboardCodes }),
  );
  const fromSection = dragging ? sectionOf(dragging) : null;
  // Исполнитель меняет только порядок: чужие секции во время перетаскивания закрыты (§2).
  const dropDisabled = (sectionId: string) => !!fromSection && !manager && sectionId !== fromSection;

  const onDragStart = (e: DragStartEvent) => {
    setDragging((e.active.data.current?.task as TaskListItem | undefined) ?? null);
  };
  const onDragEnd = (e: DragEndEvent) => {
    const t = e.active.data.current?.task as TaskListItem | undefined;
    setDragging(null);
    const over = e.over?.data.current?.over as DropOver | undefined;
    if (!t || !over) return;
    const target = dropTarget(sections, t.id!, over);
    if (!target || (target.crossSection && !manager)) return;
    move.mutate({ task: t, target, undo: currentPlace(sections, t.id!)! });
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
                onClick={async () =>
                  (await confirm({
                    title: `Удалить спринт «${sp.name}»?`,
                    body: "Его задачи вернутся в бэклог.",
                    confirmLabel: "Удалить",
                    danger: true,
                  })) && remove.mutate(sp)
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
  const loadError = project.error ?? members.error ?? snapshot.error;
  const startDay = today(tz);

  return (
    <section className="backlog-page">
      <ProjectHeader projectKey={key}>
        <Link to={`/projects/${key}/backlog/archive`}>Архив спринтов</Link>
        {manager && (
          <button type="button" className="primary" onClick={() => openDialog({ kind: "create" })}>
            Создать спринт
          </button>
        )}
      </ProjectHeader>

      {loadError && (
        <LoadError error={loadError} onRetry={() => [project, members, snapshot].forEach((q) => q.isError && void q.refetch())} />
      )}
      {snapshot.isPending && !loadError && <p className="muted">Загрузка…</p>}
      {notice && (
        <p className="notice">
          {notice}{" "}
          <button type="button" className="link" onClick={() => setNotice("")}>
            Скрыть
          </button>
        </p>
      )}

      {snapshot.isSuccess && (
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
          <DragOverlay dropAnimation={reducedMotion ? null : undefined}>
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
          error={start.isError ? startErrorText(start.error) : ""}
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
