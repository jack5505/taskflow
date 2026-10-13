import type { TaskTask, TaskUpdateRequest } from "../api/client";
import { fieldLabels, formatFieldValue, type ValueContext } from "./history";
import { compareConflict, isEmptyPatch, rebasePatch } from "./patch";
import { Modal } from "../ui/Modal";

export interface Conflict {
  /** Версия, от которой шла правка. */
  base: TaskTask;
  /** Актуальная задача из details.current. */
  current: TaskTask;
  /** Наша правка, которую сервер не принял. */
  patch: TaskUpdateRequest;
}

interface Props {
  conflict: Conflict;
  ctx: ValueContext;
  pending: boolean;
  error: string;
  /** Повторить свои изменения поверх актуальной версии. */
  onRetry: (patch: TaskUpdateRequest) => void;
  /** Оставить актуальную версию, свои изменения отбросить. */
  onDiscard: () => void;
}

/**
 * 409 VERSION_CONFLICT (§3.3): задачу уже изменил другой. Показываем его правки рядом со
 * своими и предлагаем повторить поверх актуальной версии — молча ничего не перезаписываем.
 */
export function ConflictDialog({ conflict, ctx, pending, error, onRetry, onDiscard }: Props) {
  const rows = compareConflict(conflict.base, conflict.current, conflict.patch);
  const retry = rebasePatch(conflict.current, conflict.patch);
  const nothingLeft = isEmptyPatch(retry);
  const v = (field: string, value: unknown) => formatFieldValue(field, value, ctx);
  return (
    <Modal labelledBy="conflict-title" className="conflict" onCancel={() => !pending && onDiscard()}>
      <div>
        <h2 id="conflict-title">Задачу уже изменил другой пользователь</h2>
        <p className="muted">
          Ваши изменения не сохранены. Ниже — что поменялось с тех пор, как вы открыли карточку, и что меняете вы.
        </p>
        <table className="table">
          <thead>
            <tr>
              <th>Поле</th>
              <th>Было</th>
              <th>Сейчас (чужая правка)</th>
              <th>Ваше</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.field} className={r.clash ? "clash" : undefined}>
                <th scope="row">{fieldLabels[r.field] ?? r.field}</th>
                <td>{v(r.field, r.before)}</td>
                <td>{r.changedByOthers ? v(r.field, r.theirs) : <span className="muted">без изменений</span>}</td>
                <td>{r.inMine ? v(r.field, r.mine) : <span className="muted">—</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {rows.some((r) => r.clash) && (
          <p className="hint">Выделенные поля изменили оба: повтор заменит чужое значение вашим.</p>
        )}
        {nothingLeft && <p className="notice">Ваши изменения уже есть в актуальной версии.</p>}
        {error && <p role="alert">{error}</p>}
        <div className="actions">
          {!nothingLeft && (
            <button type="button" className="primary" disabled={pending} onClick={() => onRetry(retry)} autoFocus>
              {pending ? "Сохраняем…" : "Сохранить мои изменения поверх"}
            </button>
          )}
          <button type="button" disabled={pending} onClick={onDiscard} autoFocus={nothingLeft}>
            {nothingLeft ? "Закрыть" : "Оставить актуальную версию"}
          </button>
        </div>
      </div>
    </Modal>
  );
}
