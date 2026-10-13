import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { NotificationBell } from "../notifications/NotificationBell";
import { HeaderTimer } from "../timer/HeaderTimer";
import { logout as logoutRequest } from "../api";
import { useMe } from "../auth/hooks";
import { useProject } from "../projects/hooks";
import { ProjectAvatarColors } from "../projects/ProjectAvatarColors";
import { CreateTask } from "../task/CreateTask";
import { projectKeyFromPath } from "../task/create";
import { Avatar } from "../ui/Avatar";

/** Текущий проект в шапке: ключ и название, ссылка на доску. */
function CurrentProject({ projectKey }: { projectKey: string }) {
  const project = useProject(projectKey);
  return (
    <NavLink to={`/projects/${encodeURIComponent(projectKey)}`} className="topbar-project">
      <span className="card-key">{projectKey}</span>
      {project.data?.name && <span className="topbar-project-name">{project.data.name}</span>}
    </NavLink>
  );
}

/**
 * Рамка закрытой части: разделы «Проекты / текущий проект / Отчёты» с подсветкой текущего,
 * справа таймер, уведомления, создание задачи и меню пользователя (профиль, выход).
 */
export function AppLayout() {
  const me = useMe();
  const { pathname } = useLocation();
  const projectKey = projectKeyFromPath(pathname);
  const menu = useRef<HTMLDetailsElement>(null);

  const logout = useMutation({
    // Переход на вход делает RequireAuth, кэш очищает подписка на выход в main.tsx.
    mutationFn: logoutRequest,
  });
  const name = me.data?.display_name || me.data?.email || "Профиль";
  const close = () => menu.current?.removeAttribute("open");

  // Меню закрывается щелчком мимо и Escape (фокус возвращается на аватар).
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      if (menu.current?.open && !menu.current.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && menu.current?.open) {
        close();
        menu.current.querySelector("summary")?.focus();
      }
    };
    document.addEventListener("pointerdown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, []);

  return (
    // Цвета аватаров текущего проекта — и в шапке, и на страницах: у человека один цвет.
    <ProjectAvatarColors projectKey={projectKey}>
      <div className="app">
        <header className="topbar">
          <Link to="/" className="brand">
            TaskFlow
          </Link>
          <nav className="topbar-nav" aria-label="Разделы">
            {/* «Проекты» подсвечены и на странице проекта: проект — раздел внутри них. */}
            <NavLink to="/" className={() => (pathname === "/" ? "active" : projectKey ? "active-parent" : "")}>
              Проекты
            </NavLink>
            {projectKey && <CurrentProject projectKey={projectKey} />}
            <NavLink to="/reports">Отчёты</NavLink>
          </nav>
          <div className="topbar-tools">
            <HeaderTimer />
            <NotificationBell />
            <CreateTask />
            <details className="user-menu" ref={menu}>
              <summary aria-label={`Меню пользователя: ${name}`} title={name}>
                <Avatar id={me.data?.id} name={name} />
              </summary>
              <div className="user-menu-panel" role="menu">
                <div className="user-menu-name">
                  <strong>{name}</strong>
                  {me.data?.display_name && <span className="muted">{me.data.email}</span>}
                </div>
                <Link to="/profile" role="menuitem" onClick={close}>
                  Профиль
                </Link>
                <button type="button" role="menuitem" onClick={() => logout.mutate()} disabled={logout.isPending}>
                  Выйти
                </button>
              </div>
            </details>
          </div>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </ProjectAvatarColors>
  );
}
