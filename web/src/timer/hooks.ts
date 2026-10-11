import { useEffect, useState } from "react";
import { useMutation, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { MeProfile } from "../api/client";
import { parseApiError } from "../api/errors";
import { meQueryKey } from "../auth/hooks";
import { boardQueryKey, moveKey } from "../board/cards";
import { myTasksQueryKey, projectsQueryKey } from "../projects/logic";
import { historyQueryKey, projectKeyOf, taskQueryKey } from "../task/keys";

/**
 * После старта или остановки: профиль (таймер в шапке), задачи, на которые легло время (списано,
 * журнал времени), их история, доска проекта и списки «списано» на других экранах устарели.
 * Пока идёт переход с доски, её не перечитываем: ответ откатил бы карточку, а доску перечитает
 * сам переход по завершении.
 */
export function timerChanged(qc: QueryClient, taskKeys: (string | undefined)[]) {
  void qc.invalidateQueries({ queryKey: meQueryKey });
  for (const key of new Set(taskKeys.filter((k): k is string => !!k))) {
    void qc.invalidateQueries({ queryKey: taskQueryKey(key) });
    void qc.invalidateQueries({ queryKey: historyQueryKey(key) });
    if (qc.isMutating({ mutationKey: moveKey }) === 0)
      void qc.invalidateQueries({ queryKey: boardQueryKey(projectKeyOf(key)) });
  }
  void qc.invalidateQueries({ queryKey: projectsQueryKey, refetchType: "none" });
  void qc.invalidateQueries({ queryKey: myTasksQueryKey, refetchType: "none" });
}

/** «Старт» на задаче: новый таймер сразу в профиль, шапка покажет его без ожидания GET /me. */
export function useStartTimer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (taskKey: string) => (await api.api.v1TasksTimerStartCreate(taskKey)).data,
    onSuccess: (r, taskKey) => {
      qc.setQueryData<MeProfile>(meQueryKey, (me) => (me ? { ...me, timer: r.timer ?? null } : me));
      timerChanged(qc, [taskKey, r.stopped?.task_key]);
    },
    onError: (e) => {
      // Таймер уже идёт (запущен в боте или другой вкладке) — профиль покажет его.
      if (parseApiError(e)?.code === "TIMER_ALREADY_RUNNING") void qc.invalidateQueries({ queryKey: meQueryKey });
    },
  });
}

/** «Стоп»: таймер пропадает из профиля сразу; 404 — его уже остановили (бот, автостоп, другая вкладка). */
export function useStopTimer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => (await api.api.v1MeTimerStopCreate()).data,
    onSuccess: (r) => {
      qc.setQueryData<MeProfile>(meQueryKey, (me) => (me ? { ...me, timer: null } : me));
      timerChanged(qc, [r.task_key]);
    },
    onError: (e) => {
      if (parseApiError(e)?.status === 404) {
        const key = qc.getQueryData<MeProfile>(meQueryKey)?.timer?.task_key;
        qc.setQueryData<MeProfile>(meQueryKey, (me) => (me ? { ...me, timer: null } : me));
        timerChanged(qc, [key]);
      }
    },
  });
}

/** Текущее время, обновляемое раз в interval мс, пока active. */
export function useNow(active: boolean, interval = 1000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), interval);
    return () => clearInterval(id);
  }, [active, interval]);
  return now;
}

/** Сообщение, которое само исчезает через ttl мс. */
export function useFlash(ttl = 10_000): [string, (text: string) => void] {
  const [text, setText] = useState("");
  useEffect(() => {
    if (!text) return;
    const id = setTimeout(() => setText(""), ttl);
    return () => clearTimeout(id);
  }, [text, ttl]);
  return [text, setText];
}
