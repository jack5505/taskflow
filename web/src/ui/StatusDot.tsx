/** Значок статуса задачи: круг, заполненный по ходу работы (стили .status-dot в index.css). */
export function StatusDot({ status }: { status: string }) {
  return <span className={`status-dot status-${status}`} aria-hidden="true" />;
}
