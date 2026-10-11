import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { Link } from "react-router";
import { api } from "../api";
import { errorText, fieldErrors, parseApiError } from "../api/errors";

// ТЗ §7.1: восстановление пароля — ссылка на email. Сервер отвечает одинаково для любого
// email, чтобы по ответу нельзя было узнать, есть ли такой пользователь; интерфейс тоже.
export function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const reset = useMutation({
    mutationFn: () => api.api.v1AuthPasswordResetCreate({ email: email.trim() }),
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    reset.mutate();
  };

  if (reset.isSuccess) {
    return (
      <div className="form">
        <h2>Восстановление пароля</h2>
        <p className="notice">
          Если такой email зарегистрирован, на него отправлено письмо со ссылкой для установки пароля.
          Ссылка действует 24 часа.
        </p>
        <Link to="/login">Вернуться ко входу</Link>
      </div>
    );
  }

  const err = parseApiError(reset.error);
  const emailReason = fieldErrors(err).find((f) => f.field === "email")?.reason;

  return (
    <form onSubmit={submit} className="form">
      <h2>Восстановление пароля</h2>
      <p>Укажите email — пришлём ссылку для установки нового пароля.</p>
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
      {reset.isError && <p role="alert">{emailReason ?? errorText(reset.error)}</p>}
      <button type="submit" className="primary" disabled={reset.isPending}>
        {reset.isPending ? "Отправляем…" : "Отправить ссылку"}
      </button>
      <Link to="/login">Вернуться ко входу</Link>
    </form>
  );
}
