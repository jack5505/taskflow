import { errorText } from "../api/errors";

/** Ошибка загрузки данных экрана с кнопкой «Повторить» (ТЗ §7): текст из API, повтор — перечитать. */
export function LoadError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return (
    <p role="alert" className="load-error">
      {errorText(error)}{" "}
      <button type="button" onClick={onRetry}>
        Повторить
      </button>
    </p>
  );
}
