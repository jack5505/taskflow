import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate, useParams } from "react-router";
import { api } from "../api";
import type { ProjectInviteInfo } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { useAuthStatus, useMe } from "../auth/hooks";
import { signIn } from "../auth/session";
import { MIN_PASSWORD, newPasswordError } from "../lib/password";

const ROLE_LABEL: Record<string, string> = { member: "исполнитель", manager: "менеджер" };

// Регистрация и вступление по ссылке-приглашению /join/<токен> (ТЗ §7.1). Без сессии —
// форма регистрации; с сессией — кнопка «Присоединиться». Роль уже состоящего в проекте
// не меняется.
export function JoinPage() {
  const token = useParams().token ?? "";
  const status = useAuthStatus();
  const invite = useQuery({
    queryKey: ["invite", token],
    queryFn: async ({ signal }): Promise<ProjectInviteInfo> => (await api.api.v1InvitesDetail(token, { signal })).data,
    retry: (n, e) => parseApiError(e)?.status !== 404 && n < 2,
  });

  if (invite.isPending || status === "pending") return <p className="loading">Загрузка…</p>;
  if (invite.isError) {
    const gone = parseApiError(invite.error)?.status === 404;
    return (
      <div className="form">
        <h2>Приглашение в проект</h2>
        <p role="alert">{gone ? "Ссылка-приглашение недействительна: попросите менеджера проекта прислать новую." : errorText(invite.error)}</p>
        <Link to="/login">На страницу входа</Link>
      </div>
    );
  }
  const info = invite.data;
  return status === "in" ? <Accept token={token} info={info} /> : <Register token={token} info={info} />;
}

function Heading({ info }: { info: ProjectInviteInfo }) {
  return (
    <>
      <h2>Приглашение в проект «{info.project_name}»</h2>
      <p>
        Роль в проекте: <strong>{ROLE_LABEL[info.role ?? ""]}</strong>
      </p>
    </>
  );
}

function Accept({ token, info }: { token: string; info: ProjectInviteInfo }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const accept = useMutation({
    mutationFn: async () => (await api.api.v1InvitesAcceptCreate(token)).data,
    onSuccess: async (r) => {
      // Новый проект появляется в списке, в шапке и в «Моих задачах» — обновляем всё.
      await qc.invalidateQueries();
      navigate(`/projects/${r.project_key}`, { replace: true });
    },
  });
  return (
    <div className="form">
      <Heading info={info} />
      {me.data && (
        <p className="muted">
          Вы вошли как {me.data.display_name} ({me.data.email}).
        </p>
      )}
      {accept.isError && <p role="alert">{errorText(accept.error)}</p>}
      <button type="button" className="primary" autoFocus disabled={accept.isPending} onClick={() => accept.mutate()}>
        {accept.isPending ? "Присоединяемся…" : "Присоединиться к проекту"}
      </button>
      <Link to="/">Не сейчас</Link>
    </div>
  );
}

function registerError(e: unknown): string {
  const err = parseApiError(e);
  if (err?.status === 429) {
    const min = Math.ceil(Number(err.details.retry_after_seconds) / 60);
    return min > 0 ? `Слишком много попыток регистрации. Попробуйте через ${min} мин.` : err.message;
  }
  return errorText(e);
}

function Register({ token, info }: { token: string; info: ProjectInviteInfo }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [localError, setLocalError] = useState("");

  const register = useMutation({
    mutationFn: async () => {
      const r = await api.api.v1InvitesRegisterCreate(token, { display_name: name.trim(), email: email.trim(), password });
      if (!r.data?.access_token) throw new Error("Сервер не вернул токен");
      return r.data.access_token;
    },
    onSuccess: (access) => {
      signIn(access);
      navigate(`/projects/${info.project_key}`, { replace: true });
    },
  });

  const fields = fieldErrors(parseApiError(register.error));
  const reason = (f: string) => fields.find((x) => x.field === f)?.reason;
  const general = register.isError && fields.length === 0 ? registerError(register.error) : "";
  const loginState = { from: location };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const weak = newPasswordError(password);
    if (weak) {
      setLocalError(weak);
      return;
    }
    if (password !== repeat) {
      setLocalError("Пароли не совпадают");
      return;
    }
    setLocalError("");
    register.mutate();
  };

  return (
    <form onSubmit={submit} className="form">
      <Heading info={info} />
      <p>
        Уже есть аккаунт?{" "}
        <Link to="/login" state={loginState}>
          Войдите
        </Link>
        , и вы вернётесь сюда, чтобы присоединиться.
      </p>
      <label>
        Имя
        <input required autoFocus maxLength={80} autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
        {reason("display_name") && <span role="alert">{reason("display_name")}</span>}
      </label>
      <label>
        Email
        <input
          type="email"
          required
          maxLength={254}
          autoComplete="username"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        {reason("email") && (
          <span role="alert">
            Email {reason("email")}.{" "}
            <Link to="/login" state={loginState}>
              Войти
            </Link>
          </span>
        )}
      </label>
      <label>
        Пароль (не короче {MIN_PASSWORD} символов)
        <input
          type="password"
          required
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {reason("password") && <span role="alert">{reason("password")}</span>}
      </label>
      <label>
        Повторите пароль
        <input
          type="password"
          required
          autoComplete="new-password"
          value={repeat}
          onChange={(e) => setRepeat(e.target.value)}
        />
      </label>
      {(localError || general) && <p role="alert">{localError || general}</p>}
      <button type="submit" className="primary" disabled={register.isPending}>
        {register.isPending ? "Регистрируем…" : "Зарегистрироваться и присоединиться"}
      </button>
    </form>
  );
}
