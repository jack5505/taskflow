import { useEffect, useState } from "react";
import type { MeProfile } from "../api/client";
import { errorText } from "../api/errors";
import { useMe } from "../auth/hooks";
import { useNow } from "../timer/hooks";
import { useLinkCode, useUnlinkTelegram } from "./hooks";
import { formatCountdown, secondsLeft, telegramAccount, type TelegramLinkCode } from "./logic";

/** Как часто перечитывать профиль, пока на экране живой код: привязка происходит в боте. */
const LINK_POLL_MS = 5000;

/**
 * Блок «Telegram» профиля (§7.8, §8.1): привязан — имя аккаунта и кнопка «Отвязать», нет — кнопка
 * «Привязать Telegram» с кодом и ссылкой t.me (если бот подключён). Пока код жив, профиль
 * перечитывается: после /start в боте блок сам покажет привязку.
 */
export function TelegramBlock({ me }: { me: MeProfile }) {
  const account = telegramAccount(me);
  const link = useLinkCode();
  const unlink = useUnlinkTelegram();
  // Последний выданный код держим сами: у мутации data сбрасывается на время нового запроса,
  // а при ошибке «Обновить» прежний код должен остаться на экране.
  const [code, setCode] = useState<TelegramLinkCode | null>(null);
  const request = () => link.mutate(undefined, { onSuccess: setCode });

  // Привязка состоялась — код использован, показывать его снова (после отвязки) нельзя.
  useEffect(() => {
    if (account) setCode(null);
  }, [account]);
  const alive = useAlive(code?.expires_at ?? null);
  useMe({ refetchInterval: !account && alive ? LINK_POLL_MS : undefined });

  return (
    <section className="panel">
      <h3>Telegram</h3>
      {account ? (
        <>
          <p>
            Привязан аккаунт <strong>{account}</strong>. Личные уведомления приходят в чат с ботом.
          </p>
          <div className="actions">
            <button type="button" disabled={unlink.isPending} onClick={() => unlink.mutate()}>
              Отвязать
            </button>
          </div>
          <p className="hint">
            Отвязать можно и командой <code>/unlink</code> в личном чате с ботом.
          </p>
          {unlink.isError && <p role="alert">{errorText(unlink.error)}</p>}
        </>
      ) : (
        <>
          <p className="muted">Telegram не привязан: личные уведомления не приходят, команды бота недоступны.</p>
          {!me.telegram_bot_enabled ? (
            <p className="notice">Telegram-бот пока не подключён: привязка станет доступна после его настройки.</p>
          ) : code ? (
            <LinkCode code={code} refreshing={link.isPending} onRefresh={request} />
          ) : (
            <div className="actions">
              <button type="button" className="primary" disabled={link.isPending} onClick={request}>
                Привязать Telegram
              </button>
            </div>
          )}
          {link.isError && <p role="alert">{errorText(link.error)}</p>}
        </>
      )}
    </section>
  );
}

/** Жив ли код: true до expiresAt, затем один перерендер по таймеру и false. */
function useAlive(expiresAt: string | null): boolean {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!expiresAt) return;
    const left = secondsLeft(expiresAt, Date.now());
    if (left <= 0) return;
    const id = setTimeout(() => setNow(Date.now()), left * 1000);
    return () => clearTimeout(id);
  }, [expiresAt]);
  return expiresAt !== null && secondsLeft(expiresAt, now) > 0;
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
        {left > 0 && code.url ? (
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
