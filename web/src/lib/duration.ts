// Длительности в интерфейсе (ТЗ §6.1): «2h 30m», «45m», «3h». Дни (1d = 8h) только при вводе,
// показываем часы и минуты: 10 часов работы — «10h», а не «1d 2h».

/** Минуты → «2h 30m». Ноль — «0m»; пусто и отрицательное — пустая строка. */
export function formatDuration(minutes: number | null | undefined): string {
  if (minutes == null || !Number.isFinite(minutes) || minutes < 0) return "";
  const total = Math.round(minutes);
  const h = Math.floor(total / 60);
  const m = total % 60;
  if (h === 0) return `${m}m`;
  return m === 0 ? `${h}h` : `${h}h ${m}m`;
}

/** «списано / оценка» (§7.2, §7.3): без оценки справа прочерк. */
export function formatSpentEstimate(spent: number | null | undefined, estimate: number | null | undefined): string {
  return `${formatDuration(spent ?? 0)} / ${formatDuration(estimate) || "—"}`;
}
