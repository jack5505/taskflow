import type { ReactNode } from "react";
import { NavLink } from "react-router";

import { useMe } from "../auth/hooks";
import { actorFor, useProject } from "./hooks";
import { isSystemAdmin } from "./logic";

/**
 * Заголовок страниц проекта (ТЗ §7.3, §7.4, §7.9): ключ и название, метка архива и одинаковые
 * вкладки «Доска / Бэклог / Настройки» — на доске, в бэклоге с архивом спринтов и в настройках.
 * «Настройки» — только менеджеру проекта и администратору системы (§2), как и сама страница.
 * children — то, что страница ставит справа (спринт, кнопки).
 */
export function ProjectHeader({ projectKey, children }: { projectKey: string; children?: ReactNode }) {
  const me = useMe();
  const project = useProject(projectKey);
  const settings = !!actorFor(me.data, project.data)?.manager || isSystemAdmin(me.data);
  const base = `/projects/${encodeURIComponent(projectKey)}`;
  return (
    <div className="project-head">
      <h2 className="project-title">
        <span className="card-key">{projectKey}</span> {project.data?.name}
      </h2>
      {project.data?.is_archived && <span className="badge">Архив</span>}
      <nav className="project-tabs" aria-label="Разделы проекта">
        <NavLink to={base} end>
          Доска
        </NavLink>
        <NavLink to={`${base}/backlog`}>Бэклог</NavLink>
        {settings && <NavLink to={`${base}/settings`}>Настройки</NavLink>}
      </nav>
      {children && <div className="project-head-extra">{children}</div>}
    </div>
  );
}
