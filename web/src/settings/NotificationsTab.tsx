import { useState, type FormEvent } from "react";
import type { ProjectView } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { useUpdateProject } from "./hooks";
import { GROUP_EVENTS, changedEvents, effectiveEvents, isValidTime, normalizeTime } from "./logic";
import { useToast } from "../ui/Toast";

/** Уведомления (§7.9): события бота в группе из §8.6 и время ежедневной сводки. */
export function NotificationsTab({ project, readOnly }: { project: ProjectView; readOnly: boolean }) {
  const initial = effectiveEvents(project.notification_settings);
  const initialTime = normalizeTime(project.daily_summary_time) || "10:00";
  const [events, setEvents] = useState(initial);
  const [time, setTime] = useState(initialTime);
  const toast = useToast();
  const update = useUpdateProject(project.key ?? "");

  const timeError = isValidTime(time) ? "" : "Время в формате ЧЧ:ММ";
  const serverTimeError = fieldErrors(parseApiError(update.error)).find((f) => f.field === "daily_summary_time")?.reason;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (timeError) return;
    const changes = changedEvents(initial, events);
    const body: { notification_settings?: Record<string, boolean>; daily_summary_time?: string } = {};
    if (Object.keys(changes).length > 0) body.notification_settings = changes;
    if (time !== initialTime) body.daily_summary_time = time;
    if (Object.keys(body).length === 0) return toast.success("Сохранено");
    update.mutate(body, { onSuccess: () => toast.success("Сохранено") });
  };

  return (
    <form className="form panel" onSubmit={submit}>
      <h3>Сообщения бота в группу</h3>
      {!project.telegram_chat_id && (
        <p className="muted">Группа не подключена: настройки вступят в силу после подключения.</p>
      )}
      <fieldset className="toggles" disabled={readOnly}>
        {GROUP_EVENTS.map((ev) => (
          <label key={ev.key} className="toggle">
            <input
              type="checkbox"
              checked={events[ev.key]}
              onChange={(e) => {
                setEvents({ ...events, [ev.key]: e.target.checked });
              }}
            />
            {ev.label}
          </label>
        ))}
      </fieldset>
      <label>
        Время ежедневной сводки
        <input
          type="time"
          required
          disabled={readOnly}
          value={time}
          onChange={(e) => {
            setTime(e.target.value);
          }}
          aria-invalid={timeError ? true : undefined}
        />
        <span className="hint">По часовому поясу проекта ({project.timezone}); отправляется, если сводка включена</span>
        {(timeError || serverTimeError) && <span role="alert">{timeError || serverTimeError}</span>}
      </label>
      {update.isError && !serverTimeError && <p role="alert">{errorText(update.error)}</p>}
      {!readOnly && (
        <div className="actions">
          <button type="submit" className="primary" disabled={update.isPending}>
            {update.isPending ? "Сохраняем…" : "Сохранить"}
          </button>
        </div>
      )}
    </form>
  );
}
