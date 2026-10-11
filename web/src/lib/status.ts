// Названия статусов задачи (ТЗ §4, колонки доски §7.3).
const labels: Record<string, string> = {
  todo: "К выполнению",
  in_progress: "В работе",
  review: "На проверке",
  done: "Готово",
  cancelled: "Отменена",
};

/** Русское название статуса; неизвестный показываем как есть. */
export function statusLabel(status: string | null | undefined): string {
  if (!status) return "";
  return labels[status] ?? status;
}
