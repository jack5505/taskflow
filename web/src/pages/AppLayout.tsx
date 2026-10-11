import { useMutation } from "@tanstack/react-query";
import { Link, Outlet } from "react-router";
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
        <div className="topbar-user">
          <CreateTask />
          <span>{me.data?.display_name || me.data?.email || ""}</span>
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
