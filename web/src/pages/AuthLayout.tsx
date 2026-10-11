import { Outlet } from "react-router";

/** Рамка страниц без входа: вход, восстановление и установка пароля. */
export function AuthLayout() {
  return (
    <main className="auth">
      <div className="auth-card">
        <h1 className="brand">TaskFlow</h1>
        <Outlet />
      </div>
    </main>
  );
}
