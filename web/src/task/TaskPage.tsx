import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router";
import { api } from "../api";
import type { TaskTask, TaskUpdateRequest } from "../api/client";
import { errorText } from "../api/errors";
import { meQueryKey, useMe, useTimeZone } from "../auth/hooks";
import { boardQueryKey } from "../board/cards";
import { TransitionDialog } from "../board/TransitionDialog";
import {
  STATUSES,
  canTransition,
  transitionLabel,
  transitionPrompt,
  type Status,
  type TransitionPrompt,
} from "../board/transitions";
import { statusLabel } from "../lib/status";
import { actorFor, useActiveSprint, useProject, useProjectMembers } from "../projects/hooks";
import { TimerButton } from "../timer/TimerButton";
import { canStartTimer } from "../timer/timer";
import { useSprints } from "../backlog/useSprints";
import { Attachments } from "./Attachments";
import { Comments } from "./Comments";
import { commentsQueryKey, historyQueryKey, projectKeyOf, taskQueryKey, worklogsQueryKey } from "./keys";
import { ConflictDialog, type Conflict } from "./ConflictDialog";
import { describeHistory, type ValueContext } from "./history";
import { MarkdownEditor, MarkdownView } from "./Markdown";
import { buildPatch, ifMatchBase, ifMatch, isEmptyPatch, versionConflict, type Edits } from "./patch";
import { taskPerms } from "./perms";
import { InlineEdit, TaskFields, type Save, type SaveResult } from "./TaskFields";
import { canAddWorklog } from "./worklog";
import { WorklogDialog, Worklogs } from "./Worklogs";
import { Icon, TypeIcon } from "../ui/Icon";
import { useConfirm } from "../ui/Confirm";
import { useToast } from "../ui/Toast";
import { LoadError } from "../ui/LoadError";
import { isSystemAdmin, myTasksQueryKey, projectsQueryKey } from "../projects/logic";

type Tab = "comments" | "history" | "attachments" | "worklog";
const tabs: { id: Tab; label: string }[] = [
  { id: "attachments", label: "Вложения" },
  { id: "comments", label: "Комментарии" },
  { id: "worklog", label: "Журнал времени" },
  { id: "history", label: "История" },
];

interface DialogState extends TransitionPrompt {
  to: Status;
}

/** Описание задачи: Markdown с предпросмотром; правка — по кнопке «Изменить». */
function Description({
  task,
  editable,
  saving,
  onSave,
}: {
  task: TaskTask;
  editable: boolean;
  saving: boolean;
  onSave: (d: string, base: TaskTask) => Promise<SaveResult>;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  // Версия, с которой начата правка: за минуты набора текста задачу могли изменить.
  const [base, setBase] = useState<TaskTask>(task);
  const [busy, setBusy] = useState(false);
  if (draft === null)
    return (
      <>
        {task.description ? <MarkdownView source={task.description} /> : <p className="muted">Описания нет.</p>}
        {editable && (
          <button
            type="button"
            disabled={saving}
            onClick={() => {
              setBase(task);
              setDraft(task.description ?? "");
            }}
          >
            Изменить описание
          </button>
        )}
      </>
    );
  return (
    <form
      className="form"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        const r = await onSave(draft, base);
        setBusy(false);
        if (r !== "error") setDraft(null);
      }}
    >
      <MarkdownEditor label="Описание" value={draft} onChange={setDraft} maxLength={20000} rows={12} autoFocus disabled={busy} />
      <div className="actions">
        <button type="submit" className="primary" disabled={busy}>
          {busy ? "Сохраняем…" : "Сохранить"}
        </button>
        <button type="button" disabled={busy} onClick={() => setDraft(null)}>
          Отмена
        </button>
      </div>
    </form>
  );
}

const PR_STATES: Record<string, string> = { open: "открыт", draft: "черновик", merged: "влит", closed: "закрыт" };

const LINK_KINDS: Record<string, string> = { branch: "Ветка", pr: "PR", issue: "Issue" };

/** Ветки, PR и issue GitHub задачи (§12.3, §12.9); связей нет — блока нет. */
function GitHubLinks({ taskKey }: { taskKey: string }) {
  const links = useQuery({
    queryKey: ["task", taskKey, "github"],
    queryFn: async ({ signal }) => (await api.api.v1TasksGithubList(taskKey, { signal })).data ?? [],
  });
  if (!links.data || links.data.length === 0) return null;
  return (
    <section className="panel github-links">
      <h3>GitHub</h3>
      <ul>
        {links.data.map((l) => (
          <li key={l.id}>
            {LINK_KINDS[l.kind ?? ""] ?? l.kind}
            {l.number ? ` #${l.number}` : ""}{" "}
            <a href={l.url} target="_blank" rel="noreferrer">
              {l.name}
            </a>{" "}
            <span className="muted">{l.repo}</span>
            {l.kind !== "branch" && l.state && <span className="badge">{PR_STATES[l.state] ?? l.state}</span>}
          </li>
        ))}
      </ul>
    </section>
  );
}

function History({ taskKey, ctx }: { taskKey: string; ctx: ValueContext }) {
  const history = useQuery({
    queryKey: historyQueryKey(taskKey),
    queryFn: async ({ signal }) => (await api.api.v1TasksHistoryList(taskKey, { signal })).data ?? [],
  });
  if (history.isPending) return <p className="muted">Загрузка…</p>;
  if (history.isError) return <p role="alert">{errorText(history.error)}</p>;
  if (history.data.length === 0) return <p className="muted">Изменений нет.</p>;
  return (
    <table className="table history">
      <thead>
        <tr>
          <th>Когда</th>
          <th>Кто</th>
          <th>Изменение</th>
        </tr>
      </thead>
      <tbody>
        {history.data.map((h) => {
          const line = describeHistory(h, ctx);
          return (
            <tr key={line.id}>
              <td className="nowrap">{line.when}</td>
              <td>
                {line.who}
                {line.source && <span className="badge">{line.source}</span>}
              </td>
              <td>
                {line.text}
                {line.note && <div className="muted">{line.note}</div>}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

/** Карточка задачи (ТЗ §7.5): /tasks/{key}. */
export function TaskPage() {
  const confirm = useConfirm();
  const { key = "" } = useParams();
  const projectKey = projectKeyOf(key);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const me = useMe();
  const timeZone = useTimeZone();

  const task = useQuery({
    queryKey: taskQueryKey(key),
    queryFn: async ({ signal }) => (await api.api.v1TasksDetail(key, { signal })).data,
  });
  const project = useProject(projectKey);
  const members = useProjectMembers(projectKey);
  const { sprint: activeSprint } = useActiveSprint(projectKey, project.data);

  const [tab, setTab] = useState<Tab>("comments");
  const toast = useToast();
  const [saveError, setSaveError] = useState("");
  const [conflict, setConflict] = useState<Conflict | null>(null);
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [logging, setLogging] = useState(false);
  const statusMenu = useRef<HTMLDetailsElement>(null);

  const actor = actorFor(me.data, project.data);
  const archived = project.data?.is_archived === true;
  const t = task.data;
  const perms = t ? taskPerms(t, actor, { archived, admin: me.data?.is_system_admin === true }) : null;
  const readOnly = archived || !!t?.deleted_at;
  // Задача не в активном спринте — узнаём состояние её спринта: по задачам завершённого записи
  // времени не меняются (§6.5), кнопки списания и правки не показываем.
  const otherSprint = t?.sprint_id && t.sprint_id !== activeSprint?.id ? t.sprint_id : "";
  const sprints = useSprints(otherSprint ? projectKey : "");
  const sprintClosed = !!otherSprint && sprints.data?.find((s) => s.id === otherSprint)?.state === "closed";
  const worklogReadOnly = readOnly || sprintClosed;

  const memberList = useMemo(() => members.data ?? [], [members.data]);
  const ctx: ValueContext = useMemo(() => {
    const names = new Map(memberList.map((m) => [m.user_id, m.display_name || m.email || "Без имени"]));
    return {
      userName: (id) => names.get(id),
      sprintName: (id) => (id === activeSprint?.id ? activeSprint.name : undefined),
      timeZone,
    };
  }, [memberList, activeSprint, timeZone]);

  /** После любого изменения: новая задача в кэше, история и списки других экранов устарели. */
  // Версии, которые получились из наших же сохранений и переходов в этой вкладке.
  const ownVersions = useRef(new Set<number>());
  const changed = (next?: TaskTask) => {
    if (next?.version !== undefined) ownVersions.current.add(next.version);
    if (next) queryClient.setQueryData(taskQueryKey(key), next);
    void queryClient.invalidateQueries({ queryKey: historyQueryKey(key) });
    void queryClient.invalidateQueries({ queryKey: boardQueryKey(projectKey), refetchType: "none" });
    void queryClient.invalidateQueries({ queryKey: projectsQueryKey, refetchType: "none" });
    void queryClient.invalidateQueries({ queryKey: myTasksQueryKey, refetchType: "none" });
    // Переход из «В работе», смена исполнителя и удаление останавливают таймер на задаче с
    // записью времени (§6.4). Задача в кэше уже из ответа сервера — обновляем шапку.
    const timer = me.data?.timer;
    if (timer?.task_key === key && (next?.status !== "in_progress" || next.assignee_id !== me.data?.id)) {
      void queryClient.invalidateQueries({ queryKey: meQueryKey });
      void queryClient.invalidateQueries({ queryKey: worklogsQueryKey(key) });
    }
  };

  // PATCH с If-Match версии, от которой шла правка. 409 VERSION_CONFLICT — в кэш актуальную
  // задачу, а свою правку — в окно конфликта: пользователь решит, повторять ли её (§3.3).
  const save = useMutation({
    // shownBase — версия, от которой пользователь начинал правку: при повторном конфликте
    // сравнение в окне по-прежнему идёт от неё, а не от промежуточной версии.
    mutationFn: async (v: { base: TaskTask; patch: TaskUpdateRequest; shownBase?: TaskTask }) =>
      (await api.api.v1TasksPartialUpdate(key, v.patch, { headers: ifMatch(v.base.version) })).data,
    onSuccess: (next) => {
      setConflict(null);
      changed(next);
    },
    onError: (e, v) => {
      const current = versionConflict(e);
      if (current) {
        queryClient.setQueryData(taskQueryKey(key), current);
        void queryClient.invalidateQueries({ queryKey: historyQueryKey(key) });
        setConflict({ base: v.shownBase ?? v.base, current, patch: v.patch });
      } else if (!conflict) {
        setSaveError(errorText(e));
      }
    },
  });

  const saveEdits: Save = async (edits: Edits, opened: TaskTask) => {
    const patch = buildPatch(opened, edits);
    if (isEmptyPatch(patch)) return "saved";
    // Пока поле было открыто, задачу могли изменить мы сами (другое поле, переход) — это не
    // чужая правка: If-Match берём от текущей версии. Если среди новых версий есть чужая — от
    // открытой, и сервер ответит 409.
    const base = ifMatchBase(opened, queryClient.getQueryData<TaskTask>(taskQueryKey(key)), ownVersions.current);
    setSaveError("");
    try {
      await save.mutateAsync({ base, patch });
      return "saved";
    } catch (e) {
      return versionConflict(e) ? "conflict" : "error";
    }
  };

  const transition = useMutation({
    mutationFn: async (v: { to: Status; comment?: string; assigneeId?: string }) =>
      (await api.api.v1TasksTransitionCreate(key, { to: v.to, comment: v.comment, assignee_id: v.assigneeId })).data,
    onSuccess: (next) => {
      setDialog(null);
      changed(next);
      toast.success(`${key}: ${statusLabel(next.status)}`);
      // Причина возврата или отмены сохраняется комментарием.
      void queryClient.invalidateQueries({ queryKey: commentsQueryKey(key) });
    },
    // Из окна ошибка показывается в окне.
    onError: (e) => {
      if (!dialog) toast.error(errorText(e));
    },
  });
  const take = useMutation({
    mutationFn: async () => (await api.api.v1TasksTakeCreate(key)).data,
    onSuccess: (next) => {
      changed(next);
      toast.success(`${key} взята в работу`);
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const remove = useMutation({
    mutationFn: () => api.api.v1TasksDelete(key),
    onSuccess: () => {
      changed();
      queryClient.removeQueries({ queryKey: taskQueryKey(key) });
      void navigate(`/projects/${projectKey}`);
      // Восстановить удалённую задачу может только администратор системы (§4) — ему и «Отменить».
      toast.success(
        `Задача ${key} удалена`,
        isSystemAdmin(me.data)
          ? {
              label: "Отменить",
              onClick: () =>
                void api.api.v1TasksRestoreCreate(key).then(
                  () => {
                    changed();
                    toast.success(`Задача ${key} восстановлена`);
                  },
                  (e: unknown) => toast.error(errorText(e)),
                ),
            }
          : undefined,
      );
    },
    onError: (e) => toast.error(errorText(e)),
  });
  const restore = useMutation({
    mutationFn: async () => (await api.api.v1TasksRestoreCreate(key)).data,
    onSuccess: (next) => {
      changed(next);
      toast.success(`Задача ${key} восстановлена`);
    },
    onError: (e) => toast.error(errorText(e)),
  });

  if (task.isPending) return <p className="muted">Загрузка…</p>;
  if (task.isError || !t || !perms)
    return (
      <section>
        {task.isError ? (
          <LoadError error={task.error} onRetry={() => void task.refetch()} />
        ) : (
          <p role="alert">Задача не найдена</p>
        )}
        <Link to="/" className="back-link">
          <Icon name="arrow-left" />К проектам
        </Link>
      </section>
    );

  const status = (t.status ?? "todo") as Status;
  const transitions =
    actor && !readOnly
      ? STATUSES.filter(
          (to) =>
            to !== status &&
            canTransition({ status, assigneeId: t.assignee_id, reporterId: t.reporter_id }, to, actor),
        )
      : [];

  const startTransition = (to: Status) => {
    if (statusMenu.current) statusMenu.current.open = false;
    transition.reset();
    const assigneeActive = t.assignee_id ? memberList.find((m) => m.user_id === t.assignee_id)?.is_active : undefined;
    const prompt = transitionPrompt(status, to, assigneeActive);
    if (prompt) setDialog({ to, ...prompt });
    else transition.mutate({ to });
  };

  const busy = transition.isPending || take.isPending || remove.isPending || restore.isPending;

  return (
    <section className="task-page">
      <nav className="muted" aria-label="Путь">
        <Link to={`/projects/${projectKey}`}>
          {projectKey}
          {project.data?.name ? ` — ${project.data.name}` : ""}
        </Link>
      </nav>

      {t.deleted_at && <p className="notice">Задача удалена. Восстановить может администратор системы.</p>}
      {archived && <p className="notice">Проект в архиве: карточка только для чтения.</p>}

      <div className="task-header">
        <TypeIcon type={t.type} size={20} />
        <span className="card-key">{t.key}</span>
        <h2 className="task-heading">
          <InlineEdit
            editable={perms.edit}
            label="Название"
            initial={t.title ?? ""}
            validate={(v) => {
              const n = [...v.trim()].length;
              return n < 1 || n > 200 ? "Название — от 1 до 200 символов" : "";
            }}
            task={t}
            disabled={save.isPending}
            onSave={(v, base) => saveEdits({ title: v }, base)}
          >
            {t.title}
          </InlineEdit>
        </h2>
        <div className="task-buttons">
          {transitions.length > 0 ? (
            <details className="dropdown" ref={statusMenu}>
              <summary aria-label={`Статус: ${statusLabel(status)}. Сменить`}>{statusLabel(status)}</summary>
              <div className="dropdown-menu" role="menu">
                {transitions.map((to) => (
                  <button key={to} type="button" role="menuitem" className="menu-item" disabled={busy} onClick={() => startTransition(to)}>
                    {transitionLabel(status, to)} <span className="muted">→ {statusLabel(to)}</span>
                  </button>
                ))}
              </div>
            </details>
          ) : (
            <span className="badge status-badge">{statusLabel(status)}</span>
          )}
          {perms.take && (
            <button
              type="button"
              className="primary"
              disabled={busy}
              onClick={() => {
                take.mutate();
              }}
            >
              {take.isPending ? "Берём…" : "Взять в работу"}
            </button>
          )}
          <TimerButton taskKey={key} canStart={canStartTimer(t, me.data?.id, archived)} />
          {canAddWorklog(t, actor, worklogReadOnly) && (
            <button type="button" data-testid="worklog-add" onClick={() => setLogging(true)}>
              Списать время
            </button>
          )}
          {perms.remove && (
            <button
              type="button"
              className="danger"
              disabled={busy}
              onClick={async () =>
                (await confirm({
                  title: `Удалить задачу ${t.key}?`,
                  body: "Задача пропадёт с доски, из бэклога и бота, таймер на ней остановится. Записи времени останутся в отчётах; восстановить может администратор.",
                  confirmLabel: "Удалить",
                  danger: true,
                })) && remove.mutate()
              }
            >
              Удалить
            </button>
          )}
          {perms.restore && (
            <button type="button" disabled={busy} onClick={() => restore.mutate()}>
              Восстановить
            </button>
          )}
        </div>
      </div>

      {saveError && (
        <p role="alert">
          Не сохранено: {saveError}{" "}
          <button type="button" className="link" onClick={() => setSaveError("")}>
            Скрыть
          </button>
        </p>
      )}

      <div className="task-layout">
        <div className="task-main">
          <section className="block">
            <h3>Описание</h3>
            <Description
              task={t}
              editable={perms.edit}
              saving={save.isPending}
              onSave={(d, base) => saveEdits({ description: d }, base)}
            />
          </section>

          <GitHubLinks taskKey={key} />

          <nav className="tabs" role="tablist" aria-label="Разделы задачи">
            {tabs.map((x) => (
              <button
                key={x.id}
                type="button"
                role="tab"
                aria-selected={tab === x.id}
                className={tab === x.id ? "tab active" : "tab"}
                onClick={() => setTab(x.id)}
              >
                {x.label}
              </button>
            ))}
          </nav>
          <div className="tab-body" role="tabpanel">
            {tab === "comments" && (
              <Comments
                taskKey={key}
                actor={actor}
                canWrite={perms.comment}
                archived={readOnly}
                timeZone={timeZone}
              />
            )}
            {tab === "history" && <History taskKey={key} ctx={ctx} />}
            {tab === "attachments" && (
              <Attachments taskKey={key} actor={actor} canWrite={perms.comment} archived={readOnly} timeZone={timeZone} />
            )}
            {tab === "worklog" && (
              <Worklogs task={t} actor={actor} readOnly={worklogReadOnly} sprintClosed={sprintClosed} timeZone={timeZone} />
            )}
          </div>
        </div>

        <aside className="task-side panel">
          <TaskFields
            task={t}
            perms={perms}
            members={memberList}
            ctx={ctx}
            activeSprint={activeSprint}
            timeZone={timeZone}
            saving={save.isPending}
            onSave={saveEdits}
          />
        </aside>
      </div>

      {dialog && (
        <TransitionDialog
          task={t}
          to={dialog.to}
          comment={dialog.comment}
          chooseAssignee={dialog.chooseAssignee}
          activeMembers={memberList.filter((m) => m.is_active !== false)}
          pending={transition.isPending}
          error={transition.isError ? errorText(transition.error) : ""}
          onCancel={() => setDialog(null)}
          onSubmit={(comment, assigneeId) => transition.mutate({ to: dialog.to, comment, assigneeId })}
        />
      )}

      {logging && (
        <WorklogDialog task={t} manager={!!actor?.manager} timeZone={timeZone} onClose={() => setLogging(false)} />
      )}

      {conflict && (
        <ConflictDialog
          conflict={conflict}
          ctx={ctx}
          pending={save.isPending}
          error={save.isError && !versionConflict(save.error) ? errorText(save.error) : ""}
          onRetry={(patch) => save.mutate({ base: conflict.current, patch, shownBase: conflict.base })}
          onDiscard={() => setConflict(null)}
        />
      )}
    </section>
  );
}
