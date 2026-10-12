import { useSearchParams } from "react-router";
import type { MeProfile } from "../api/client";
import { errorText } from "../api/errors";
import { useGitHubConnect, useGitHubUnlink } from "./hooks";

/**
 * GitHub в профиле (§7.8, §12.5): связан — логин и «Отвязать»; нет — «Привязать GitHub» (вход
 * в GitHub и возврат сюда). Итог возврата приходит в адресе: ?github=linked | error&message=….
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
      {result === "error" && <p role="alert">{params.get("message") || "Не удалось привязать GitHub"}</p>}
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
        <>
          <p className="muted">GitHub не привязан. Привяжите аккаунт, чтобы pull request'ы назначали задачи на вас.</p>
          <div className="actions">
            <button type="button" className="primary" disabled={connect.isPending} onClick={() => connect.mutate()}>
              Привязать GitHub
            </button>
          </div>
        </>
      )}
      {(connect.isError || unlink.isError) && <p role="alert">{errorText(connect.error ?? unlink.error)}</p>}
    </section>
  );
}
