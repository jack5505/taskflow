import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link, useNavigate, useSearchParams } from "react-router";
import { api } from "../api";
import { errorText, fieldErrors, parseApiError } from "../api/errors";

const MIN_PASSWORD = 10; // ТЗ §7.1

// Установка пароля по ссылке из письма /password-set?token=… (ТЗ §7.1, §9.1): и для нового
// пользователя, и для восстановления. Ссылка одноразовая, действует 24 часа.
export function PasswordSetPage() {
  const navigate = useNavigate();
  const token = useSearchParams()[0].get("token") ?? "";
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [localError, setLocalError] = useState("");

  const set = useMutation({
    mutationFn: () => api.api.v1AuthPasswordSetCreate({ token, password }),
    onSuccess: () =>
      navigate("/login", { replace: true, state: { notice: "Пароль задан. Войдите с новым паролем." } }),
  });

  const fields = fieldErrors(parseApiError(set.error));
  const tokenReason = fields.find((f) => f.field === "token")?.reason;
  const passwordReason = fields.find((f) => f.field === "password")?.reason;

  if (!token || tokenReason) {
    return (
      <div className="form">
        <h2>Установка пароля</h2>
        <p role="alert">Ссылка недействительна или уже использована. Запросите новую.</p>
        <Link to="/forgot-password">Запросить новую ссылку</Link>
      </div>
    );
  }

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (password.length < MIN_PASSWORD) {
      setLocalError(`Пароль должен быть не короче ${MIN_PASSWORD} символов`);
      return;
    }
    if (password !== repeat) {
      setLocalError("Пароли не совпадают");
      return;
    }
    setLocalError("");
    set.mutate();
  };

  const message = localError || passwordReason || (set.isError ? errorText(set.error) : "");

  return (
    <form onSubmit={submit} className="form">
      <h2>Установка пароля</h2>
      <p>Придумайте пароль не короче {MIN_PASSWORD} символов.</p>
      <label>
        Новый пароль
        <input
          type="password"
          autoComplete="new-password"
          required
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </label>
      <label>
        Повторите пароль
        <input
          type="password"
          autoComplete="new-password"
          required
          value={repeat}
          onChange={(e) => setRepeat(e.target.value)}
        />
      </label>
      {message && <p role="alert">{message}</p>}
      <button type="submit" className="primary" disabled={set.isPending}>
        {set.isPending ? "Сохраняем…" : "Задать пароль"}
      </button>
    </form>
  );
}
