import { Link } from "react-router";
import { errorText, parseApiError } from "../api/errors";
import { useMe } from "../auth/hooks";
import { useFlash, useNow, useStopTimer } from "./hooks";
import { elapsedSeconds, formatClock, stopMessage } from "./timer";

/**
 * Запущенный таймер в шапке на всех страницах (ТЗ §6.4): ключ задачи, прошедшее время и «Стоп».
 * После остановки таймер исчезает, на его месте ненадолго — сколько списано.
 */
export function HeaderTimer() {
  // Раз в минуту сверяемся с сервером: таймер могли запустить или остановить в боте.
  const me = useMe({ refetchInterval: 60_000 });
  const timer = me.data?.timer;
  const now = useNow(!!timer);
  const stop = useStopTimer();
  const [flash, setFlash] = useFlash();

  const message = flash ? (
    <span className="header-timer muted" role="status">
      {flash}
    </span>
  ) : null;
  if (!timer?.task_key) return message;

  const onStop = () =>
    stop.mutate(undefined, {
      onSuccess: (r) => setFlash(stopMessage(r)),
      onError: (e) =>
        setFlash(parseApiError(e)?.status === 404 ? "Таймер уже остановлен" : `Не остановлен: ${errorText(e)}`),
    });

  return (
    <>
      <span className="header-timer running" title={timer.title}>
        <span className="timer-dot" aria-hidden="true" />
        <Link to={`/tasks/${timer.task_key}`} className="card-key">
          {timer.task_key}
        </Link>
        <span className="timer-clock" aria-label="Прошло времени">
          {formatClock(elapsedSeconds(timer, me.dataUpdatedAt, now))}
        </span>
        <button type="button" disabled={stop.isPending} onClick={onStop}>
          {stop.isPending ? "Стоп…" : "Стоп"}
        </button>
      </span>
      {message}
    </>
  );
}
