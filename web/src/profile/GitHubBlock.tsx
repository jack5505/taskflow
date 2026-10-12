import { useSearchParams } from "react-router";
import type { MeProfile } from "../api/client";
import { errorText } from "../api/errors";
import { useGitHubConnect, useGitHubUnlink } from "./hooks";

/** Причины неудачи возврата из GitHub (?github=error&reason=…) — текст свой, не из адреса. */
const REASONS: Record<string, string> = {
  state: "Ссылка привязки устарела или открыта в другом браузере — начните привязку заново.",
  taken: "Этот GitHub-аккаунт уже связан с другим пользователем.",
  inactive: "Пользователь деактивирован — привязка невозможна.",
  failed: "GitHub не ответил — попробуйте ещё раз.",
};

/**
 * GitHub в профиле (§7.8, §12.5): связан — логин и «Отвязать»; нет — «Привязать GitHub» (вход
 * в GitHub и возврат сюда, если вход App настроен). Итог возврата — в адресе: ?github=linked | error&reason=….
 */
export function GitHubBlock({ me }: { me: MeProfile }) {
  const [params] = useSearchParams();
  const connect = useGitHubConnect();
  const unlink = useGitHubUnlink();
  const result = params.get("github");
  const login = me.github_login;

  return (
    <section className="panel">
      <h3>GitHub</h3>
      {result === "linked" && login && <p className="notice">GitHub-аккаунт @{login} привязан.</p>}
      {result === "error" && <p role="alert">{REASONS[params.get("reason") ?? ""] ?? REASONS.failed}</p>}
      {login ? (
        <>
          <p>
            Привязан аккаунт{" "}
            <a href={`https://github.com/${login}`} target="_blank" rel="noreferrer">
              <strong>@{login}</strong>
            </a>
            . Ваши pull request'ы и ветки отмечаются в истории задач от вашего имени.
          </p>
          <div className="actions">
            <button type="button" disabled={unlink.isPending} onClick={() => unlink.mutate()}>
              Отвязать
            </button>
          </div>
        </>
      ) : (
        me.github_login_enabled && (
        <>
          <p className="muted">GitHub не привязан. Привяжите аккаунт, чтобы pull request'ы назначали задачи на вас.</p>
          <div className="actions">
            <button type="button" className="primary" disabled={connect.isPending} onClick={() => connect.mutate()}>
              Привязать GitHub
            </button>
          </div>
        </>
        )
      )}
      {(connect.isError || unlink.isError) && <p role="alert">{errorText(connect.error ?? unlink.error)}</p>}
    </section>
  );
}
