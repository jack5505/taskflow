import { useMemo, useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { api } from "../api";
import type { ProjectView } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { projectQueryKey } from "../projects/hooks";
import { projectsQueryKey, timeZones } from "../projects/logic";
import { useUpdateProject } from "./hooks";

/**
 * Общее (§7.9): название, описание, часовой пояс; архивация и мягкое удаление — только
 * администратор системы (§3.2).
 */
export function GeneralTab({ project, admin }: { project: ProjectView; admin: boolean }) {
  const key = project.key ?? "";
  const readOnly = !!project.is_archived;
  const [name, setName] = useState(project.name ?? "");
  const [description, setDescription] = useState(project.description ?? "");
  const [timezone, setTimezone] = useState(project.timezone ?? "");
  const [saved, setSaved] = useState(false);
  const zones = useMemo(() => timeZones(), []);
  const qc = useQueryClient();
  const navigate = useNavigate();

  const update = useUpdateProject(key);
  const archive = useUpdateProject(key);
  const remove = useMutation({
    mutationFn: () => api.api.v1ProjectsDelete(key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: projectsQueryKey });
      qc.removeQueries({ queryKey: projectQueryKey(key) });
      navigate("/");
    },
  });

  const fields = fieldErrors(parseApiError(update.error));
  const reason = (f: string) => fields.find((e) => e.field === f)?.reason;
  const generalError = update.isError && fields.length === 0 ? errorText(update.error) : "";

  const edit = (set: (v: string) => void, v: string) => {
    set(v);
    setSaved(false);
    if (update.isError) update.reset();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    // Только изменённые поля: PATCH меняет переданное.
    const body: { name?: string; description?: string; timezone?: string } = {};
    if (name.trim() !== (project.name ?? "")) body.name = name.trim();
    if (description.trim() !== (project.description ?? "")) body.description = description.trim();
    if (timezone.trim() !== (project.timezone ?? "")) body.timezone = timezone.trim();
    if (Object.keys(body).length === 0) return setSaved(true);
    update.mutate(body, { onSuccess: () => setSaved(true) });
  };

  const adminError = archive.error ?? remove.error;

  return (
    <>
      <form className="form panel" onSubmit={submit}>
        <label>
          Название
          <input required maxLength={100} disabled={readOnly} value={name} onChange={(e) => edit(setName, e.target.value)} />
          {reason("name") && <span role="alert">{reason("name")}</span>}
        </label>
        <label>
          Описание
          <textarea
            rows={4}
            maxLength={2000}
            disabled={readOnly}
            value={description}
            onChange={(e) => edit(setDescription, e.target.value)}
          />
          {reason("description") && <span role="alert">{reason("description")}</span>}
        </label>
        <label>
          Часовой пояс проекта
          <input
            list="settings-tz-list"
            maxLength={64}
            required
            disabled={readOnly}
            value={timezone}
            onChange={(e) => edit(setTimezone, e.target.value)}
          />
          <datalist id="settings-tz-list">
            {zones.map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
          <span className="hint">По нему отправляется ежедневная сводка и считаются сроки</span>
          {reason("timezone") && <span role="alert">{reason("timezone")}</span>}
        </label>
        {generalError && <p role="alert">{generalError}</p>}
        {!readOnly && (
          <div className="actions">
            <button type="submit" className="primary" disabled={update.isPending}>
              {update.isPending ? "Сохраняем…" : "Сохранить"}
            </button>
            {saved && <span className="muted">Сохранено</span>}
          </div>
        )}
      </form>

      {admin && (
        <section className="panel">
          <h3>Архив и удаление</h3>
          <p className="muted">
            В архивном проекте изменения запрещены, доска и карточки открываются только для чтения. Удалённый проект
            пропадает из списков, бота и отчётов; данные сохраняются, восстановить его можно в списке проектов.
          </p>
          {adminError && <p role="alert">{errorText(adminError)}</p>}
          <div className="actions">
            <button
              type="button"
              disabled={archive.isPending}
              onClick={() => archive.mutate({ is_archived: !project.is_archived })}
            >
              {project.is_archived ? "Вернуть из архива" : "Архивировать"}
            </button>
            <button
              type="button"
              className="danger"
              disabled={remove.isPending}
              onClick={() => window.confirm(`Удалить проект ${key}?`) && remove.mutate()}
            >
              Удалить проект
            </button>
          </div>
        </section>
      )}
    </>
  );
}
