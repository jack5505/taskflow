import { useState } from "react";
import type { MeProfile } from "../api/client";
import { errorText } from "../api/errors";
import { useNow } from "../timer/hooks";
import { requestLinkCode, useLinkCode } from "./hooks";
import { formatCountdown, secondsLeft, telegramAccount, type TelegramLinkCode } from "./logic";

/**
 * Блок «Telegram» профиля (§7.8, §8.1): привязан — имя аккаунта, нет — кнопка «Привязать Telegram»
 * с кодом и ссылкой t.me. Выдачи кода и отвязки в API пока нет (issue #49): кнопки появятся вместе
 * с эндпоинтами, до тех пор блок показывает состояние и как привязать или отвязать через бота.
 */
export function TelegramBlock({ me }: { me: MeProfile }) {
  const account = telegramAccount(me);
  const link = useLinkCode();
  // Последний выданный код держим сами: у мутации data сбрасывается на время нового запроса,
  // а при ошибке «Обновить» прежний код должен остаться на экране.
  const [code, setCode] = useState<TelegramLinkCode | null>(null);
  const request = () => link.mutate(undefined, { onSuccess: setCode });

  return (
    <section className="panel">
      <h3>Telegram</h3>
      {account ? (
        <>
          <p>
            Привязан аккаунт <strong>{account}</strong>. Личные уведомления приходят в чат с ботом.
          </p>
          <p className="hint">
            Отвязать — команда <code>/unlink</code> в личном чате с ботом.
          </p>
        </>
      ) : (
        <>
          <p className="muted">Telegram не привязан: личные уведомления не приходят, команды бота недоступны.</p>
          {requestLinkCode ? (
            code ? (
              <LinkCode code={code} refreshing={link.isPending} onRefresh={request} />
            ) : (
              <div className="actions">
                <button type="button" className="primary" disabled={link.isPending} onClick={request}>
                  Привязать Telegram
                </button>
              </div>
            )
          ) : (
            <p className="notice">Получение кода привязки в профиле появится в ближайшем обновлении.</p>
          )}
          {link.isError && <p role="alert">{errorText(link.error)}</p>}
        </>
      )}
    </section>
  );
}

/**
 * Код привязки со сроком действия (§8.1: 8 символов, 10 минут): ссылка на бота, сам код текстом,
 * обратный отсчёт; истёкший код зачёркнут, новый — по кнопке «Обновить».
 */
export function LinkCode({
  code,
  refreshing,
  onRefresh,
}: {
  code: TelegramLinkCode;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  // Отсчёт идёт, пока код жив; истёк — таймер больше не нужен.
  const now = useNow(secondsLeft(code.expires_at, Date.now()) > 0);
  const left = secondsLeft(code.expires_at, now);

  return (
    <div className="link-code">
      <p>
        Откройте ссылку и нажмите «Запустить» или отправьте боту в личном чате <code>/start {code.code}</code>.
      </p>
      <div className="link-code-row">
        <span className={left > 0 ? "link-code-value" : "link-code-value expired"}>{code.code}</span>
        {left > 0 ? (
          <a className="link-code-open" href={code.url} target="_blank" rel="noreferrer">
            Открыть в Telegram
          </a>
        ) : null}
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
