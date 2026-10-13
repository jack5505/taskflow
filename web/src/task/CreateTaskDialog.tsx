import { useRef, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { SprintView, TaskTask } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { useMe } from "../auth/hooks";
import { PRIORITIES, TASK_TYPES, priorityLabels, typeLabels } from "../board/filters";
import { useProjectMembers, useProjectsList } from "../projects/hooks";
import {
  affectedByCreate,
  apiFieldErrors,
  canManageProject,
  creatableProjects,
  emptyForm,
  openSprints,
  sprintsQueryKey,
  validateCreate,
  type CreateForm,
  type FormErrors,
} from "./create";
import { MarkdownEditor } from "./Markdown";
import { DateField } from "../ui/DateField";
import { useConfirm } from "../ui/Confirm";
import { Modal } from "../ui/Modal";

interface Props {
  /** Проект текущей страницы — предвыбран (§7.6). */
  projectKey: string;
  onCreated: (t: TaskTask) => void;
  onCancel: () => void;
}

const sprintStates: Record<string, string> = { active: "активный", planned: "запланирован" };

/** Окно «Новая задача» (ТЗ §7.6). Исполнитель и спринт — только менеджеру и администратору (§2). */
export function CreateTaskDialog({ projectKey, onCreated, onCancel }: Props) {
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const me = useMe();
  const [form, setForm] = useState<CreateForm>(() => emptyForm(projectKey));
  const [localErrors, setLocalErrors] = useState<FormErrors>({});
  const dueRef = useRef<HTMLInputElement>(null);

  // Тот же запрос и кэш, что у списка проектов на главной: администратор видит все проекты.
  const projects = useProjectsList("active");
  const options = creatableProjects(projects.data);
  // Текущий проект в архиве или ещё не загружен список — выбора нет, пусть выберут сами.
  const selected = options.find((p) => p.key === form.projectKey);
  const key = selected?.key ?? "";
  const manage = canManageProject(me.data, selected);

  const members = useProjectMembers(manage ? key : "");
  const sprints = useQuery({
    queryKey: sprintsQueryKey(key),
    queryFn: async ({ signal }): Promise<SprintView[]> => (await api.api.v1ProjectsSprintsList(key, { signal })).data ?? [],
    enabled: manage && key !== "",
  });

  const create = useMutation({
    mutationFn: async (v: { key: string; body: ReturnType<typeof validateCreate>["body"] }) =>
      (await api.api.v1ProjectsTasksCreate(v.key, v.body)).data,
    onSuccess: (t, v) => {
      void queryClient.invalidateQueries({ predicate: (q) => affectedByCreate(q.queryKey, v.key) === "refetch" });
      void queryClient.invalidateQueries({
        predicate: (q) => affectedByCreate(q.queryKey, v.key) === "stale",
        refetchType: "none",
      });
      onCreated(t);
    },
  });

  const apiError = parseApiError(create.error);
  const serverErrors = create.isError ? apiFieldErrors(fieldErrors(apiError), manage) : {};
  const errors: FormErrors = { ...serverErrors, ...localErrors };
  const general =
    errors.general ?? (create.isError && Object.keys(serverErrors).length === 0 ? errorText(create.error) : "");

  // Ошибки относятся к отправленным значениям: при правке поля его ошибку убираем.
  const set = <K extends keyof CreateForm>(k: K, v: CreateForm[K]) => {
    setForm((f) => {
      const next = { ...f, [k]: v };
      // Исполнитель и спринт принадлежат проекту: при смене проекта сбрасываются.
      if (k === "projectKey") Object.assign(next, { assigneeId: "", sprintId: "" });
      return next;
    });
    setLocalErrors({});
    if (create.isError) create.reset();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (create.isPending) return;
    const { body, errors: errs } = validateCreate({ ...form, projectKey: key }, manage);
    setLocalErrors(errs);
    if (Object.keys(errs).length === 0) create.mutate({ key, body });
  };

  // Пока идёт запрос, окно не закрывается: иначе ответ закрыл бы уже следующее окно, а
  // пользователь мог бы создать задачу повторно. Случайный щелчок мимо окна или Escape не
  // выбрасывает набранное без вопроса.
  const dirty = JSON.stringify(form) !== JSON.stringify(emptyForm(projectKey));
  const dismiss = async () => {
    if (create.isPending) return;
    if (dirty && !(await confirm({ title: "Закрыть окно?", body: "Введённые данные не сохранятся.", confirmLabel: "Закрыть" })))
      return;
    onCancel();
  };

  const err = (f: keyof FormErrors) =>
    errors[f] ? (
      <span role="alert" className="field-error">
        {errors[f]}
      </span>
    ) : null;
  const invalid = (f: keyof FormErrors) => (errors[f] ? true : undefined);

  return (
    <Modal labelledBy="create-task-title" className="create-task" onCancel={() => void dismiss()}>
      <form
        className="form"
        noValidate
        onSubmit={submit}
        onKeyDown={(e) => {
          // Ctrl+Enter — создать из любого поля, в том числе из описания.
          if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) e.currentTarget.requestSubmit();
        }}
      >
        <h2 id="create-task-title">Новая задача</h2>

        <label>
          Проект
          <select
            value={key}
            onChange={(e) => set("projectKey", e.target.value)}
            disabled={projects.isPending}
            aria-invalid={invalid("project")}
          >
            {!selected && <option value="">{projects.isPending ? "Загрузка…" : "— выберите —"}</option>}
            {options.map((p) => (
              <option key={p.key} value={p.key}>
                {p.key} — {p.name}
              </option>
            ))}
          </select>
          {projects.isError && <span role="alert">{errorText(projects.error)}</span>}
          {err("project")}
        </label>

        <label>
          Название
          <input
            data-autofocus
            value={form.title}
            onChange={(e) => set("title", e.target.value)}
            aria-invalid={invalid("title")}
          />
          {err("title")}
        </label>

        <div className="form-row">
          <label>
            Тип
            <select value={form.type} onChange={(e) => set("type", e.target.value as CreateForm["type"])}>
              {TASK_TYPES.map((v) => (
                <option key={v} value={v}>
                  {typeLabels[v]}
                </option>
              ))}
            </select>
            {err("type")}
          </label>
          <label>
            Приоритет
            <select value={form.priority} onChange={(e) => set("priority", e.target.value as CreateForm["priority"])}>
              {PRIORITIES.map((v) => (
                <option key={v} value={v}>
                  {priorityLabels[v]}
                </option>
              ))}
            </select>
            {err("priority")}
          </label>
        </div>

        <div className="field">
          <MarkdownEditor
            label="Описание"
            value={form.description}
            onChange={(v) => set("description", v)}
            // Лимит §3.3 — 20 000 символов, его проверяет validateCreate; maxLength считает
            // UTF-16, а не символы, поэтому здесь только верхняя граница с запасом.
            maxLength={40000}
            rows={5}
          />
          {err("description")}
        </div>

        {manage && (
          <div className="form-row">
            <label>
              Исполнитель
              <select
                value={form.assigneeId}
                onChange={(e) => set("assigneeId", e.target.value)}
                aria-invalid={invalid("assignee")}
              >
                <option value="">— не назначен —</option>
                {(members.data ?? [])
                  .filter((m) => m.is_active !== false)
                  .map((m) => (
                    <option key={m.user_id} value={m.user_id}>
                      {m.display_name || m.email}
                    </option>
                  ))}
              </select>
              {err("assignee")}
            </label>
            <label>
              Спринт
              <select
                value={form.sprintId}
                onChange={(e) => set("sprintId", e.target.value)}
                aria-invalid={invalid("sprint")}
              >
                <option value="">Бэклог</option>
                {openSprints(sprints.data).map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                    {s.state && sprintStates[s.state] ? ` (${sprintStates[s.state]})` : ""}
                  </option>
                ))}
              </select>
              {err("sprint")}
            </label>
          </div>
        )}

        <div className="form-row">
          <label>
            Оценка
            <input
              placeholder="2h 30m"
              value={form.estimate}
              onChange={(e) => set("estimate", e.target.value)}
              aria-invalid={invalid("estimate")}
            />
            {err("estimate")}
          </label>
          <label>
            Срок
            <DateField
              ref={dueRef}
              value={form.dueDate}
              onChange={(v) => set("dueDate", v)}
              aria-invalid={invalid("dueDate")}
            />
            {err("dueDate")}
          </label>
        </div>

        <label>
          Метки
          <input
            placeholder="через запятую"
            value={form.labels}
            onChange={(e) => set("labels", e.target.value)}
            aria-invalid={invalid("labels")}
          />
          {err("labels")}
        </label>

        {general && <p role="alert">{general}</p>}
        <div className="actions">
          <button type="submit" className="primary" disabled={create.isPending || projects.isPending}>
            {create.isPending ? "Создаём…" : "Создать"}
          </button>
          <button type="button" onClick={onCancel} disabled={create.isPending}>
            Отмена
          </button>
        </div>
      </form>
    </Modal>
  );
}
