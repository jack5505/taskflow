import { Navigate, Outlet, useLocation, type Location } from "react-router";
import { useAuthStatus } from "./hooks";

/** Закрытая часть приложения: без сессии — на страницу входа с возвратом после него. */
export function RequireAuth() {
  const status = useAuthStatus();
  const location = useLocation();
  if (status === "pending") return <p className="loading">Загрузка…</p>;
  if (status === "out") return <Navigate to="/login" replace state={{ from: location }} />;
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
