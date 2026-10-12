import { useState } from "react";
import type { ProjectView } from "../api/client";
import { errorText } from "../api/errors";
import { useGitHubRepos, useSetGitHubRepo } from "./hooks";

/**
 * GitHub (§12.1, §12.7): привязан — репозиторий и кнопка «Отвязать»; нет — выбор из репозиториев,
 * в которые установлен GitHub App. Один репозиторий на проект и один проект на репозиторий.
 */
export function GitHubTab({
  project,
  readOnly,
}: {
  project: ProjectView;
  readOnly: boolean;
}) {
  const key = project.key ?? "";
  const bound = project.github_repo;
  const repos = useGitHubRepos(!bound && !readOnly);
  const set = useSetGitHubRepo(key);
  const [choice, setChoice] = useState("");

  return (
    <section className="panel settings-text">
      <h3>Репозиторий GitHub</h3>
      {bound ? (
        <>
          <p>
            Привязан репозиторий{" "}
            <a
              href={`https://github.com/${bound}`}
              target="_blank"
              rel="noreferrer"
            >
              <strong>{bound}</strong>
            </a>
            . Ветки и pull request'ы с ключом задачи ({key}-42) связываются с
            задачами, PR двигает статус.
          </p>
          {!readOnly && (
            <div className="actions">
              <button
                type="button"
                disabled={set.isPending}
                onClick={() => set.mutate(null)}
              >
                Отвязать
              </button>
            </div>
          )}
        </>
      ) : (
        <>
          <p className="muted">Репозиторий не привязан.</p>
          {!readOnly && (
            <>
              {repos.isError && <p role="alert">{errorText(repos.error)}</p>}
              {repos.data && repos.data.length === 0 && (
                <p className="notice">
                  GitHub App не установлен ни в один репозиторий — установите
                  его на GitHub.
                </p>
              )}
              {repos.data && repos.data.length > 0 && (
                <div className="actions">
                  <label>
                    Репозиторий{" "}
                    <select
                      value={choice}
                      onChange={(e) => setChoice(e.target.value)}
                    >
                      <option value="">— выберите —</option>
                      {repos.data.map((r) => (
                        <option key={r.full_name} value={r.full_name}>
                          {r.full_name}
                          {r.private ? " (приватный)" : ""}
                        </option>
                      ))}
                    </select>
                  </label>
                  <button
                    type="button"
                    className="primary"
                    disabled={!choice || set.isPending}
                    onClick={() => set.mutate(choice)}
                  >
                    Привязать
                  </button>
                </div>
              )}
              {repos.isPending && (
                <p className="muted">Загрузка репозиториев…</p>
              )}
            </>
          )}
        </>
      )}
      {set.isError && <p role="alert">{errorText(set.error)}</p>}
    </section>
  );
}
