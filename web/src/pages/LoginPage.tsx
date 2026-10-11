import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link, useLocation, useNavigate, type Location } from "react-router";
import { api } from "../api";
import { errorText, parseApiError } from "../api/errors";
import { signIn } from "../auth/session";

type LoginState = { from?: Location; notice?: string } | null;

// ТЗ §7.1: вход по email и паролю. 401 — неверная пара или пользователь деактивирован,
// 429 — вход закрыт после серии неудачных попыток, сервер сообщает, сколько ждать.
function loginError(e: unknown): string {
  const err = parseApiError(e);
  if (err?.status === 401) return err.message || "Неверный email или пароль";
  if (err?.status === 429) {
    const sec = Number(err.details.retry_after_seconds);
    if (sec > 0) {
      const min = Math.ceil(sec / 60);
      return `Слишком много неудачных попыток входа. Попробуйте снова через ${min} мин.`;
    }
    return err.message || "Слишком много неудачных попыток входа, подождите";
  }
  return errorText(e);
}

export function LoginPage() {
  const navigate = useNavigate();
  const state = useLocation().state as LoginState;
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const login = useMutation({
    mutationFn: async () => (await api.api.v1AuthLoginCreate({ email: email.trim(), password })).data,
    onSuccess: (session) => {
      if (!session.access_token) return;
      signIn(session.access_token);
      navigate(state?.from ?? "/", { replace: true });
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate();
  };

  return (
    <form onSubmit={submit} className="form">
      <h2>Вход</h2>
      {state?.notice && <p className="notice">{state.notice}</p>}
      <label>
        Email
        <input
          type="email"
          autoComplete="username"
          required
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
      </label>
      <label>
        Пароль
        <input
          type="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      {login.isError && <p role="alert">{loginError(login.error)}</p>}
      <button type="submit" className="primary" disabled={login.isPending}>
        {login.isPending ? "Входим…" : "Войти"}
      </button>
      <Link to="/forgot-password">Забыли пароль?</Link>
    </form>
  );
}
