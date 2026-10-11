import { useEffect, useState } from "react";
import type { ProjectView } from "../api/client";
import { errorText } from "../api/errors";
import { useMe } from "../auth/hooks";
import { formatCountdown, secondsLeft } from "../profile/logic";
import { useProject } from "../projects/hooks";
import { useNow } from "../timer/hooks";
import { useBindCode, useDisconnectGroup } from "./hooks";
import { groupLabel, type GroupBindCode } from "./logic";

/** Как часто перечитывать проект, пока на экране живой код: подключение происходит в группе. */
const BIND_POLL_MS = 5000;

/**
 * Telegram (§7.9, §8.2): подключена — название группы и кнопка «Отключить»; нет — кнопка
 * «Подключить Telegram-группу» с кодом для /bind (если бот подключён). Пока код жив, проект
 * перечитывается: после /bind в группе вкладка сама покажет подключение.
 */
export function TelegramTab({ project, readOnly }: { project: ProjectView; readOnly: boolean }) {
  const key = project.key ?? "";
  const me = useMe();
  const bind = useBindCode(key);
  const disconnect = useDisconnectGroup(key);
  // Последний выданный код держим сами: при ошибке «Обновить» прежний код остаётся на экране.
  const [code, setCode] = useState<GroupBindCode | null>(null);
  const request = () => bind.mutate(undefined, { onSuccess: setCode });
  const label = groupLabel(project);

  // Группа подключена — код использован, показывать его снова (после отключения) нельзя.
  useEffect(() => {
    if (label) setCode(null);
  }, [label]);
  // Отсчёт идёт, пока код жив; истёк — таймер больше не нужен.
  const now = useNow(code !== null && secondsLeft(code.expires_at, Date.now()) > 0);
  const alive = code !== null && secondsLeft(code.expires_at, now) > 0;
  useProject(key, { refetchInterval: !label && alive ? BIND_POLL_MS : undefined });

  return (
    <section className="panel settings-text">
      <h3>Telegram-группа</h3>
      {label ? (
        <>
          <p>
            Подключена группа <strong>{label}</strong>. Сообщения о задачах проекта {key} публикуются в ней.
          </p>
          {!readOnly && (
            <div className="actions">
              <button type="button" disabled={disconnect.isPending} onClick={() => disconnect.mutate()}>
                Отключить
              </button>
            </div>
          )}
          <p className="hint">
            Отключить можно и командой <code>/unbind</code> в группе (если в ней несколько проектов —{" "}
            <code>/unbind {key}</code>). Если удалить бота из группы, она отключится сама.
          </p>
          {disconnect.isError && <p role="alert">{errorText(disconnect.error)}</p>}
        </>
      ) : (
        <>
          <p className="muted">Группа не подключена.</p>
          {!me.data?.telegram_bot_enabled ? (
            <p className="notice">Telegram-бот пока не подключён: подключить группу можно после его настройки.</p>
          ) : code ? (
            <BindCode code={code} now={now} refreshing={bind.isPending} onRefresh={request} />
          ) : (
            !readOnly && (
              <div className="actions">
                <button type="button" className="primary" disabled={bind.isPending} onClick={request}>
                  Подключить Telegram-группу
                </button>
              </div>
            )
          )}
          {bind.isError && <p role="alert">{errorText(bind.error)}</p>}
        </>
      )}
    </section>
  );
}

/**
 * Код подключения (§8.2: 8 символов, 15 минут) и порядок действий в Telegram; истёкший код
 * зачёркнут, новый — по кнопке «Обновить».
 */
function BindCode({
  code,
  now,
  refreshing,
  onRefresh,
}: {
  code: GroupBindCode;
  now: number;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const left = secondsLeft(code.expires_at, now);
  return (
    <div className="link-code">
      <ol>
        <li>
          Добавьте бота{code.bot_username ? <strong> @{code.bot_username}</strong> : null} в группу Telegram — права
          администратора группы ему не нужны.
        </li>
        <li>
          Отправьте в группе команду <code>{code.command}</code>. Отправить её должен менеджер проекта с привязанным в
          профиле Telegram.
        </li>
      </ol>
      <div className="link-code-row">
        <span className={left > 0 ? "link-code-value" : "link-code-value expired"}>{code.code}</span>
        <button type="button" disabled={refreshing} onClick={onRefresh}>
          {refreshing ? "Обновляем…" : "Обновить"}
        </button>
      </div>
      <p className={left > 0 ? "hint" : "hint expired"} aria-live="polite">
        {left > 0 ? `Код действует ещё ${formatCountdown(left)}` : "Код истёк — получите новый"}
      </p>
    </div>
  );
}
