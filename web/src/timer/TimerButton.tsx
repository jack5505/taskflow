import { errorText, parseApiError } from "../api/errors";
import { useMe } from "../auth/hooks";
import { useFlash, useStartTimer, useStopTimer } from "./hooks";
import { startMessage, stopMessage } from "./timer";
import { Icon } from "../ui/Icon";

/**
 * Кнопка «Старт / Стоп» таймера на задаче (ТЗ §6.4, §7.5): «Стоп» — если таймер идёт на этой
 * задаче, «Старт» — если canStart (исполнитель, задача в работе, см. canStartTimer).
 * Итог — сколько списано, ошибка сервера — рядом с кнопкой.
 */
export function TimerButton({ taskKey, canStart, className }: { taskKey: string; canStart: boolean; className?: string }) {
  const me = useMe();
  const running = me.data?.timer?.task_key === taskKey;
  const start = useStartTimer();
  const stop = useStopTimer();
  const [flash, setFlash] = useFlash();
  const busy = start.isPending || stop.isPending;

  if (!running && !canStart && !flash) return null;
  const onClick = () => {
    setFlash("");
    if (running)
      stop.mutate(undefined, {
        onSuccess: (r) => setFlash(stopMessage(r)),
        onError: (e) => setFlash(parseApiError(e)?.status === 404 ? "Таймер уже остановлен" : errorText(e)),
      });
    else
      start.mutate(taskKey, {
        onSuccess: (r) => setFlash(startMessage(r)),
        onError: (e) =>
          setFlash(parseApiError(e)?.code === "TIMER_ALREADY_RUNNING" ? "Таймер на этой задаче уже идёт" : errorText(e)),
      });
  };
  return (
    <span className={className ? `timer-control ${className}` : "timer-control"}>
      {(running || canStart) && (
        <button
          type="button"
          className={running ? "timer-stop" : "timer-start"}
          disabled={busy}
          onClick={onClick}
          aria-label={running ? `Остановить таймер ${taskKey}` : `Запустить таймер ${taskKey}`}
        >
          <Icon name={running ? "stop" : "play"} size={12} />
          {running ? (stop.isPending ? "Стоп…" : "Стоп") : start.isPending ? "Старт…" : "Старт"}
        </button>
      )}
      {flash && (
        <span className="muted timer-flash" role="status">
          {flash}
        </span>
      )}
    </span>
  );
}
