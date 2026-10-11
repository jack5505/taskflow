import { useMemo, useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../api";
import type { ProjectView } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { DEFAULT_TIMEZONE, normalizeKeyInput, projectKeyError, timeZones } from "./logic";

interface Props {
  onCreated: (p: ProjectView) => void;
  onCancel: () => void;
}

/** Форма «Создать проект» (администратор системы, ТЗ §3.2): ключ, название, описание, пояс. */
export function CreateProjectForm({ onCreated, onCancel }: Props) {
  const [key, setKey] = useState("");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [timezone, setTimezone] = useState(DEFAULT_TIMEZONE);
  const [keyLocalError, setKeyLocalError] = useState("");
  const zones = useMemo(() => timeZones(), []);

  const create = useMutation({
    mutationFn: async () =>
      (
        await api.api.v1ProjectsCreate({
          key,
          name: name.trim(),
          description: description.trim() || undefined,
          timezone: timezone.trim() || undefined,
        })
      ).data,
    onSuccess: onCreated,
  });

  const fields = fieldErrors(parseApiError(create.error));
  const reason = (f: string) => fields.find((e) => e.field === f)?.reason;
  const keyError = keyLocalError || reason("key");
  const generalError = create.isError && fields.length === 0 ? errorText(create.error) : "";

  // Ошибки сервера относятся к отправленным значениям: при правке формы их убираем.
  const edit = (set: (v: string) => void, value: string) => {
    set(value);
    if (create.isError) create.reset();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const err = projectKeyError(key);
    setKeyLocalError(err);
    if (!err) create.mutate();
  };

  return (
    <form onSubmit={submit} className="form panel">
      <h2>Новый проект</h2>
      <label>
        Ключ
        <input
          required
          autoFocus
          maxLength={10}
          placeholder="TM"
          value={key}
          onChange={(e) => {
            edit(setKey, normalizeKeyInput(e.target.value));
            setKeyLocalError("");
          }}
          aria-invalid={keyError ? true : undefined}
        />
        <span className="hint">2–10 заглавных латинских букв, после создания не меняется</span>
        {keyError && <span role="alert">{keyError}</span>}
      </label>
      <label>
        Название
        <input required maxLength={100} value={name} onChange={(e) => edit(setName, e.target.value)} />
        {reason("name") && <span role="alert">{reason("name")}</span>}
      </label>
      <label>
        Описание
        <textarea rows={3} maxLength={2000} value={description} onChange={(e) => edit(setDescription, e.target.value)} />
        {reason("description") && <span role="alert">{reason("description")}</span>}
      </label>
      <label>
        Часовой пояс
        <input list="tz-list" maxLength={64} value={timezone} onChange={(e) => edit(setTimezone, e.target.value)} />
        <datalist id="tz-list">
          {zones.map((z) => (
            <option key={z} value={z} />
          ))}
        </datalist>
        {reason("timezone") && <span role="alert">{reason("timezone")}</span>}
      </label>
      {generalError && <p role="alert">{generalError}</p>}
      <div className="actions">
        <button type="submit" className="primary" disabled={create.isPending}>
          {create.isPending ? "Создаём…" : "Создать"}
        </button>
        <button type="button" onClick={onCancel}>
          Отмена
        </button>
      </div>
    </form>
  );
}
