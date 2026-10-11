import { useMutation } from "@tanstack/react-query";
import { Link, Outlet } from "react-router";
import { NotificationBell } from "../notifications/NotificationBell";
import { HeaderTimer } from "../timer/HeaderTimer";
import { logout as logoutRequest } from "../api";
import { useMe } from "../auth/hooks";
import { CreateTask } from "../task/CreateTask";

/** Рамка закрытой части: шапка с именем пользователя и выходом. */
export function AppLayout() {
  const me = useMe();

  const logout = useMutation({
    // Переход на вход делает RequireAuth, кэш очищает подписка на выход в main.tsx.
    mutationFn: logoutRequest,
  });

  return (
    <div className="app">
      <header className="topbar">
        <Link to="/" className="brand">
          TaskFlow
        </Link>
        <div className="topbar-tools">
          <HeaderTimer />
          <NotificationBell />
        </div>
        <div className="topbar-user">
          <CreateTask />
          <Link to="/profile" title="Профиль">
            {me.data?.display_name || me.data?.email || "Профиль"}
          </Link>
          <button type="button" onClick={() => logout.mutate()} disabled={logout.isPending}>
            Выйти
          </button>
        </div>
      </header>
      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}
