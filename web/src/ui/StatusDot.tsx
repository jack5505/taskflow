/**
 * Значок статуса задачи: круг, заполненный по ходу работы (стили .status-dot в index.css).
 * Неизвестный или пустой статус — серый круг: .status-dot без модификатора.
 */
export function StatusDot({ status }: { status?: string | null }) {
  return <span className={status ? `status-dot status-${status}` : "status-dot"} aria-hidden="true" />;
}
