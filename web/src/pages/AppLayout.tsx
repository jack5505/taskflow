import { useMutation } from "@tanstack/react-query";
import { Link, Outlet } from "react-router";
import { api, csrfHeaders } from "../api";
import { useMe } from "../auth/hooks";
import { signOut } from "../auth/session";

/** Рамка закрытой части: шапка с именем пользователя и выходом. */
export function AppLayout() {
  const me = useMe();

  const logout = useMutation({
    mutationFn: () => api.api.v1AuthLogoutCreate({ headers: csrfHeaders }),
    // Сессию в памяти сбрасываем и при ошибке: пользователь попросил выйти. Cookie сервер
    // уже мог погасить, а мог и нет — тогда она истечёт сама, access-токена в памяти нет.
    // Кэш запросов очищается подпиской в main.tsx.
    onSettled: () => signOut(),
  });

  return (
    <div className="app">
      <header className="topbar">
        <Link to="/" className="brand">
          TaskFlow
        </Link>
        <div className="topbar-user">
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
