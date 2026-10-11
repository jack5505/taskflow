import { Navigate, Outlet, useLocation, type Location } from "react-router";
import { useAuthStatus } from "./hooks";
import { getSignOutReason } from "./session";

/** Закрытая часть приложения: без сессии — на страницу входа с возвратом после него. */
export function RequireAuth() {
  const status = useAuthStatus();
  const location = useLocation();
  if (status === "pending") return <p className="loading">Загрузка…</p>;
  if (status === "out") {
    // После «Выйти» — без возврата: следующий вошедший не должен попасть на страницу прежнего.
    const state = getSignOutReason() === "logout" ? null : { from: location };
    return <Navigate to="/login" replace state={state} />;
  }
  return <Outlet />;
}

/** Вход для уже вошедшего пользователя не нужен — туда, откуда пришёл, иначе на главную. */
export function RequireGuest() {
  const status = useAuthStatus();
  const from = (useLocation().state as { from?: Location } | null)?.from;
  if (status === "pending") return <p className="loading">Загрузка…</p>;
  if (status === "in") return <Navigate to={from ?? "/"} replace />;
  return <Outlet />;
}
