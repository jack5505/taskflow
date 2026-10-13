import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { TaskTask, TaskWorklogView } from "../api/client";
import { errorText } from "../api/errors";
import { addDays, today as todayIn } from "../backlog/logic";
import { boardQueryKey } from "../board/cards";
import type { Actor } from "../board/transitions";
import { formatDuration } from "../lib/duration";
import { formatDate } from "../lib/format";
import { myTasksQueryKey, projectsQueryKey } from "../projects/logic";
import { reportQueryKey } from "../reports/logic";
import { projectKeyOf, taskQueryKey, worklogsQueryKey } from "./keys";
import {
  EDIT_WINDOW_DAYS,
  MAX_WORKLOG_COMMENT,
  dayOf,
  sourceLabels,
  validateDraft,
  worklogErrorText,
  worklogPerms,
  type DraftErrors,
  type WorklogDraft,
} from "./worklog";
import { DateField } from "../ui/DateField";
import { useConfirm } from "../ui/Confirm";
import { useToast } from "../ui/Toast";
import { LoadError } from "../ui/LoadError";
import { Modal } from "../ui/Modal";

/**
 * После списания, правки или удаления: список записей и задача (списано, остаток) перечитываются,
 * доска, «Мои задачи», проекты и отчёты — при следующем показе.
 */
function worklogsChanged(qc: QueryClient, taskKey: string) {
  void qc.invalidateQueries({ queryKey: worklogsQueryKey(taskKey) });
  void qc.invalidateQueries({ queryKey: taskQueryKey(taskKey) });
  void qc.invalidateQueries({ queryKey: boardQueryKey(projectKeyOf(taskKey)), refetchType: "none" });
  void qc.invalidateQueries({ queryKey: projectsQueryKey, refetchType: "none" });
  void qc.invalidateQueries({ queryKey: myTasksQueryKey, refetchType: "none" });
  void qc.invalidateQueries({ queryKey: reportQueryKey, refetchType: "none" });
}

interface DialogProps {
  task: TaskTask;
  /** Правка записи; без неё — новое списание. */
  worklog?: TaskWorklogView;
  /** Менеджер правит без ограничения срока (§6.5). */
  manager: boolean;
  timeZone?: string;
  onClose: () => void;
}

/** Окно «Списать время» (§6.3) и правки записи (§6.5). */
export function WorklogDialog({ task, worklog, manager, timeZone, onClose }: DialogProps) {
  const toast = useToast();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const key = task.key ?? "";
  const today = todayIn(timeZone);
  const created = dayOf(task.created_at, timeZone);
  // Автор переносит свою запись не дальше 7 дней назад — иначе потеряет право её править.
  const editFrom = worklog && !manager ? addDays(today, -EDIT_WINDOW_DAYS) : "";
  const minDate = created > editFrom ? created : editFrom;
  const [draft, setDraft] = useState<WorklogDraft>({
    duration: worklog ? formatDuration(worklog.minutes) : "",
    workDate: worklog?.work_date ?? today,
    comment: worklog?.comment ?? "",
  });
  const [errors, setErrors] = useState<DraftErrors>({});

  const save = useMutation({
    mutationFn: async (d: WorklogDraft) => {
      const body = { duration: d.duration.trim(), work_date: d.workDate, comment: d.comment.trim() };
      if (!worklog) return (await api.api.v1TasksWorklogsCreate(key, body)).data;
      // Правка — только изменённые поля: неизменная старая дата не должна упереться в проверки.
      return (
        await api.api.v1WorklogsPartialUpdate(worklog.id!, {
          duration: body.duration !== formatDuration(worklog.minutes) ? body.duration : undefined,
          work_date: body.work_date !== worklog.work_date ? body.work_date : undefined,
          comment: body.comment !== (worklog.comment ?? "") ? body.comment : undefined,
        })
      ).data;
    },
    onSuccess: () => {
      worklogsChanged(queryClient, key);
      onClose();
      toast.success(worklog ? "Запись времени изменена" : `Время списано на ${key}`);
    },
  });

  // Пока запрос идёт, окно не закрывается: иначе его ответ закрыл бы уже новое окно, а повторное
  // списание задвоило бы запись. Введённое не теряется молча.
  const dirty = worklog
    ? draft.duration !== formatDuration(worklog.minutes) || draft.workDate !== worklog.work_date || draft.comment !== (worklog.comment ?? "")
    : draft.duration.trim() !== "" || draft.comment.trim() !== "";
  const dismiss = async () => {
    if (save.isPending) return;
    if (dirty && !(await confirm({ title: "Закрыть без сохранения?", body: "Введённое пропадёт.", confirmLabel: "Закрыть" })))
      return;
    onClose();
  };

  const set = (field: keyof WorklogDraft, value: string) => {
    setDraft((d) => ({ ...d, [field]: value }));
    setErrors((e) => ({ ...e, [field]: undefined }));
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const errs = validateDraft(draft, { today, minDate: minDate || undefined, keepDate: worklog?.work_date });
    setErrors(errs);
    if (Object.values(errs).every((v) => !v)) save.mutate(draft);
  };

  return (
    <Modal labelledBy="worklog-title" onCancel={() => void dismiss()}>
      <form
        className="form"
        data-testid="worklog-form"
        noValidate
        onSubmit={submit}
      >
        <h2 id="worklog-title">{worklog ? "Изменить запись времени" : "Списать время"}</h2>
        <p className="muted">
          {task.key} «{task.title}»
        </p>
        <label>
          Длительность
          <input
            autoFocus
            data-testid="worklog-duration"
            placeholder="2h 30m"
            maxLength={100}
            value={draft.duration}
            aria-invalid={!!errors.duration}
            onChange={(e) => set("duration", e.target.value)}
          />
          {errors.duration ? (
            <span className="field-error">{errors.duration}</span>
          ) : (
            <span className="hint">Например: 2h 30m, 45m, 3h, 1d (1d = 8h)</span>
          )}
        </label>
        <label>
          Дата работы
          <DateField
            data-testid="worklog-date"
            value={draft.workDate}
            max={today}
            min={minDate || undefined}
            aria-invalid={!!errors.workDate}
            onChange={(v) => set("workDate", v)}
          />
          {errors.workDate && <span className="field-error">{errors.workDate}</span>}
        </label>
        <label>
          Комментарий
          <textarea
            rows={3}
            data-testid="worklog-comment"
            // Длина в символах (эмодзи — один символ) проверяется validateDraft; maxLength считает UTF-16.
            maxLength={MAX_WORKLOG_COMMENT * 2}
            value={draft.comment}
            aria-invalid={!!errors.comment}
            onChange={(e) => set("comment", e.target.value)}
          />
          {errors.comment ? (
            <span className="field-error">{errors.comment}</span>
          ) : (
            <span className="hint">Необязательно, до 500 символов</span>
          )}
        </label>
        {save.isError && (
          <p role="alert" data-testid="worklog-error">
            {worklogErrorText(save.error)}
          </p>
        )}
        <div className="actions">
          <button type="submit" className="primary" data-testid="worklog-submit" disabled={save.isPending}>
            {save.isPending ? "Сохраняем…" : worklog ? "Сохранить" : "Списать"}
          </button>
          <button type="button" disabled={save.isPending} onClick={dismiss}>
            Отмена
          </button>
        </div>
      </form>
    </Modal>
  );
}

function WorklogRow({
  w,
  task,
  actor,
  readOnly,
  today,
  timeZone,
  onEdit,
}: {
  w: TaskWorklogView;
  task: TaskTask;
  actor: Actor | null;
  readOnly: boolean;
  today: string;
  timeZone?: string;
  onEdit: () => void;
}) {
  const toast = useToast();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const key = task.key ?? "";
  const perms = worklogPerms(w, actor, { readOnly, cancelled: task.status === "cancelled", today });
  const remove = useMutation({
    mutationFn: () => api.api.v1WorklogsDelete(w.id!),
    onSuccess: () => {
      worklogsChanged(queryClient, key);
      toast.success("Запись времени удалена");
    },
  });
  return (
    <tr data-testid="worklog-row" data-worklog-id={w.id}>
      <td>{w.user_name || "Пользователь"}</td>
      <td className="nowrap">{formatDate(w.work_date, timeZone)}</td>
      <td className="nowrap" data-testid="worklog-duration-cell">
        {formatDuration(w.minutes)}
      </td>
      <td>
        <span className="badge">{sourceLabels[w.source ?? "manual"]}</span>
      </td>
      <td className="worklog-comment">
        {w.comment}
        {remove.isError && (
          <p role="alert" data-testid="worklog-error">
            {errorText(remove.error)}
          </p>
        )}
      </td>
      <td className="nowrap">
        {perms.edit && (
          <button type="button" className="link" data-testid="worklog-edit" onClick={onEdit}>
            Изменить
          </button>
        )}{" "}
        {perms.remove && (
          <button
            type="button"
            className="link danger"
            data-testid="worklog-delete"
            disabled={remove.isPending}
            onClick={async () =>
              (await confirm({ title: `Удалить запись ${formatDuration(w.minutes)}?`, confirmLabel: "Удалить", danger: true })) &&
              remove.mutate()
            }
          >
            Удалить
          </button>
        )}
      </td>
    </tr>
  );
}

interface Props {
  task: TaskTask;
  actor: Actor | null;
  /** Проект в архиве, задача удалена или её спринт завершён (§6.5). */
  readOnly: boolean;
  /** Спринт задачи завершён — пояснение над журналом. */
  sprintClosed: boolean;
  timeZone?: string;
}

/** Вкладка «Журнал времени» (§7.5): кто, дата, длительность, источник, комментарий; правка по §6.5. */
export function Worklogs({ task, actor, readOnly, sprintClosed, timeZone }: Props) {
  const key = task.key ?? "";
  const list = useQuery({
    queryKey: worklogsQueryKey(key),
    queryFn: async ({ signal }) => (await api.api.v1TasksWorklogsList(key, { signal })).data ?? [],
  });
  const [editing, setEditing] = useState<TaskWorklogView | null>(null);
  const today = todayIn(timeZone);

  const closedNote = sprintClosed && (
    <p className="notice">Спринт задачи завершён: записи времени не добавляются, не изменяются и не удаляются.</p>
  );
  if (list.isPending) return <p className="muted">Загрузка…</p>;
  if (list.isError) return <LoadError error={list.error} onRetry={() => void list.refetch()} />;
  if (list.data.length === 0)
    return (
      <>
        {closedNote}
        <p className="muted" data-testid="worklog-empty">
          Время на задачу ещё не списано.
        </p>
      </>
    );
  const total = list.data.reduce((s, w) => s + (w.minutes ?? 0), 0);
  return (
    <>
      {closedNote}
      <table className="table worklogs" data-testid="worklog-list">
        <thead>
          <tr>
            <th>Кто</th>
            <th>Дата</th>
            <th>Длительность</th>
            <th>Источник</th>
            <th>Комментарий</th>
            <th aria-label="Действия" />
          </tr>
        </thead>
        <tbody>
          {list.data.map((w) => (
            <WorklogRow
              key={w.id}
              w={w}
              task={task}
              actor={actor}
              readOnly={readOnly}
              today={today}
              timeZone={timeZone}
              onEdit={() => setEditing(w)}
            />
          ))}
        </tbody>
        <tfoot>
          <tr>
            <th colSpan={2}>Всего</th>
            <th className="nowrap" data-testid="worklog-total">
              {formatDuration(total)}
            </th>
            <th colSpan={3} />
          </tr>
        </tfoot>
      </table>
      {editing && (
        <WorklogDialog
          task={task}
          worklog={editing}
          manager={!!actor?.manager}
          timeZone={timeZone}
          onClose={() => setEditing(null)}
        />
      )}
    </>
  );
}
