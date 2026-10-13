import { useMemo, useState, type FormEvent } from "react";
import type { MeProfile } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { useMe } from "../auth/hooks";
import { timeZones } from "../projects/logic";
import { useLogoutAll, usePasswordChange, usePasswordLink, useUpdateMe } from "./hooks";
import { MIN_PASSWORD } from "../lib/password";
import {
  PERSONAL_EVENTS,
  changedNotifications,
  displayNameError,
  effectiveNotifications,
  isKnownTimeZone,
  passwordChangeErrors,
  profileChanges,
  retryAfterText,
} from "./logic";
import { GitHubBlock } from "./GitHubBlock";
import { TelegramBlock } from "./TelegramBlock";
import { useConfirm } from "../ui/Confirm";
import { useToast } from "../ui/Toast";
import { LoadError } from "../ui/LoadError";

/** Профиль (ТЗ §7.8): имя и часовой пояс, пароль и сессии, Telegram, личные уведомления §8.7. */
export function ProfilePage() {
  const me = useMe();

  let body;
  if (me.isError) body = <LoadError error={me.error} onRetry={() => void me.refetch()} />;
  else if (!me.data) body = <p className="muted">Загрузка…</p>;
  else
    body = (
      <>
        <AccountForm me={me.data} />
        <SecurityBlock me={me.data} />
        <TelegramBlock me={me.data} />
        {(me.data.github_login_enabled || me.data.github_login) && <GitHubBlock me={me.data} />}
        <NotificationsForm me={me.data} />
      </>
    );

  return (
    <section className="profile-page">
      <div className="block-head">
        <h2>Профиль</h2>
        {me.data?.email && <span className="muted">{me.data.email}</span>}
      </div>
      {body}
    </section>
  );
}

/**
 * Имя и часовой пояс: отправляются только поля, изменённые в форме. Сравниваем с тем, что было
 * в форме при открытии (или после сохранения), а не с последним профилем: профиль могли изменить
 * в другой вкладке, и нетронутое поле не должно молча вернуть прежнее значение.
 */
function AccountForm({ me }: { me: MeProfile }) {
  const [base, setBase] = useState({ display_name: me.display_name ?? "", timezone: me.timezone ?? "" });
  const [name, setName] = useState(base.display_name);
  const [timezone, setTimezone] = useState(base.timezone);
  const toast = useToast();
  const [touched, setTouched] = useState(false);
  const zones = useMemo(() => timeZones(), []);
  const update = useUpdateMe();

  const fields = fieldErrors(parseApiError(update.error));
  const reason = (f: string) => fields.find((e) => e.field === f)?.reason;
  const nameError = touched ? displayNameError(name) : "";
  const tzError = touched && !isKnownTimeZone(timezone, zones) ? "Выберите часовой пояс из списка" : "";
  const generalError = update.isError && fields.length === 0 ? errorText(update.error) : "";

  const edit = (set: (v: string) => void, v: string) => {
    set(v);
    if (update.isError) update.reset();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (displayNameError(name) || !isKnownTimeZone(timezone, zones)) return;
    const body = profileChanges(base, name, timezone);
    if (Object.keys(body).length === 0) return toast.success("Сохранено");
    update.mutate(body, {
      onSuccess: (fresh) => {
        // Форма — с тем, что сохранил сервер (имя он обрезает).
        const next = { display_name: fresh.display_name ?? "", timezone: fresh.timezone ?? "" };
        setBase(next);
        setName(next.display_name);
        setTimezone(next.timezone);
        toast.success("Сохранено");
      },
    });
  };

  return (
    <form className="form panel" onSubmit={submit} noValidate>
      <h3>Основное</h3>
      <label>
        Имя
        <input
          autoComplete="name"
          value={name}
          onChange={(e) => edit(setName, e.target.value)}
          aria-invalid={nameError || reason("display_name") ? true : undefined}
        />
        {(nameError || reason("display_name")) && <span role="alert">{nameError || reason("display_name")}</span>}
      </label>
      <label>
        Часовой пояс
        <input
          list="profile-tz-list"
          maxLength={64}
          placeholder="Начните вводить: Moscow, Tashkent…"
          value={timezone}
          onChange={(e) => edit(setTimezone, e.target.value)}
          aria-invalid={tzError || reason("timezone") ? true : undefined}
        />
        <datalist id="profile-tz-list">
          {zones.map((z) => (
            <option key={z} value={z} />
          ))}
        </datalist>
        <span className="hint">В нём показываются даты и время, по нему бот присылает напоминания о сроках</span>
        {(tzError || reason("timezone")) && <span role="alert">{tzError || reason("timezone")}</span>}
      </label>
      {generalError && <p role="alert">{generalError}</p>}
      <div className="actions">
        <button type="submit" className="primary" disabled={update.isPending}>
          {update.isPending ? "Сохраняем…" : "Сохранить"}
        </button>
      </div>
    </form>
  );
}

/**
 * Пароль и сеансы (§7.8, §9.1). Смена пароля — с вводом текущего: после неё сервер отзывает все
 * остальные сессии, эта остаётся. Забытый текущий пароль — ссылка на почту, как при восстановлении.
 * «Выйти везде» отзывает сессии сразу, в том числе эту.
 */
function SecurityBlock({ me }: { me: MeProfile }) {
  const confirm = useConfirm();
  const logoutAll = useLogoutAll();
  return (
    <section className="panel">
      <h3>Пароль и сеансы</h3>
      <PasswordChangeForm email={me.email ?? ""} />
      {logoutAll.isError && <p role="alert">{errorText(logoutAll.error, "Не удалось выйти на всех устройствах")}</p>}
      <div className="actions">
        <button
          type="button"
          className="danger"
          disabled={logoutAll.isPending}
          onClick={async () =>
            (await confirm({
              title: "Выйти на всех устройствах?",
              body: "Завершатся все сеансы, включая этот: войти придётся заново везде.",
              confirmLabel: "Выйти везде",
              danger: true,
            })) && logoutAll.mutate()
          }
        >
          Выйти везде
        </button>
      </div>
    </section>
  );
}

const emptyPasswords = { current: "", next: "", repeat: "" };

function PasswordChangeForm({ email }: { email: string }) {
  const confirm = useConfirm();
  const [form, setForm] = useState(emptyPasswords);
  const [touched, setTouched] = useState(false);
  const toast = useToast();
  const change = usePasswordChange();
  const link = usePasswordLink();

  const apiError = parseApiError(change.error);
  const fields = fieldErrors(apiError);
  const local = touched ? passwordChangeErrors(form) : {};
  const serverCurrent = fields.find((f) => f.field === "current_password")?.reason;
  const serverNext = fields.find((f) => f.field === "new_password")?.reason;
  const errors = { current: local.current || serverCurrent, next: local.next || serverNext, repeat: local.repeat };
  // Ошибка без поля формы (429, сеть, иное поле) — общей строкой, чтобы отказ не пропал молча.
  const generalError =
    change.isError && !serverCurrent && !serverNext
      ? [errorText(change.error), apiError?.status === 429 ? retryAfterText(apiError.details) : ""].filter(Boolean).join(" ")
      : "";
  const linkError = link.isError
    ? (fieldErrors(parseApiError(link.error)).find((f) => f.field === "email")?.reason ??
      errorText(link.error, "Не удалось отправить письмо, попробуйте ещё раз"))
    : "";

  const edit = (key: keyof typeof form, v: string) => {
    setForm({ ...form, [key]: v });
    if (change.isError) change.reset();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (Object.keys(passwordChangeErrors(form)).length > 0) return;
    change.mutate(
      { current: form.current, next: form.next },
      {
        onSuccess: () => {
          setForm(emptyPasswords);
          setTouched(false);
          toast.success("Пароль изменён. Остальные сеансы завершены.");
        },
      },
    );
  };

  const input = (key: keyof typeof form, label: string, autoComplete: string) => (
    <label>
      {label}
      <input
        type="password"
        autoComplete={autoComplete}
        value={form[key]}
        onChange={(e) => edit(key, e.target.value)}
        aria-invalid={errors[key] ? true : undefined}
      />
      {errors[key] && <span role="alert">{errors[key]}</span>}
    </label>
  );

  return (
    <form className="form" onSubmit={submit} noValidate aria-label="Смена пароля">
      {input("current", "Текущий пароль", "current-password")}
      {input("next", "Новый пароль", "new-password")}
      {input("repeat", "Повторите новый пароль", "new-password")}
      <span className="hint">
        Не короче {MIN_PASSWORD} символов. После смены пароля остальные сеансы завершатся, этот останется.
      </span>
      {generalError && <p role="alert">{generalError}</p>}
      <div className="actions">
        <button type="submit" className="primary" disabled={change.isPending}>
          {change.isPending ? "Сохраняем…" : "Сменить пароль"}
        </button>
        <button
          type="button"
          className="link"
          disabled={link.isPending || !email}
          onClick={async () =>
            (await confirm({
              title: "Отправить ссылку для нового пароля?",
              body: `Письмо со ссылкой придёт на ${email}. Текущий пароль действует, пока вы не зададите новый.`,
              confirmLabel: "Отправить",
            })) && link.mutate(email)
          }
        >
          Забыли текущий пароль?
        </button>
      </div>
      {link.isSuccess && (
        <p className="notice">
          Письмо со ссылкой отправлено на {email}. Ссылка действует 24 часа; после установки нового пароля потребуется
          войти заново на всех устройствах. Если письма нет, проверьте спам и повторите не раньше чем через минуту.
        </p>
      )}
      {linkError && <p role="alert">{linkError}</p>}
    </form>
  );
}

/**
 * Личные уведомления §8.7: по умолчанию все включены, отправляются только переключатели,
 * изменённые в форме (база — как в AccountForm: состояние при открытии или после сохранения).
 */
function NotificationsForm({ me }: { me: MeProfile }) {
  const [base, setBase] = useState(() => effectiveNotifications(me.notification_settings));
  const [events, setEvents] = useState(base);
  const toast = useToast();
  const update = useUpdateMe();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const changes = changedNotifications(base, events);
    if (Object.keys(changes).length === 0) return toast.success("Сохранено");
    update.mutate(
      { notification_settings: changes },
      {
        onSuccess: (fresh) => {
          const next = effectiveNotifications(fresh.notification_settings);
          setBase(next);
          setEvents(next);
          toast.success("Сохранено");
        },
      },
    );
  };

  return (
    <form className="form panel" onSubmit={submit}>
      <h3>Уведомления в Telegram</h3>
      {!me.telegram_user_id && (
        <p className="muted">Telegram не привязан: настройки вступят в силу после привязки.</p>
      )}
      <fieldset className="toggles">
        {PERSONAL_EVENTS.map((ev) => (
          <label key={ev.key} className="toggle">
            <input
              type="checkbox"
              checked={events[ev.key]}
              onChange={(e) => {
                setEvents({ ...events, [ev.key]: e.target.checked });
                if (update.isError) update.reset();
              }}
            />
            {ev.label}
          </label>
        ))}
      </fieldset>
      {update.isError && <p role="alert">{errorText(update.error)}</p>}
      <div className="actions">
        <button type="submit" className="primary" disabled={update.isPending}>
          {update.isPending ? "Сохраняем…" : "Сохранить"}
        </button>
      </div>
    </form>
  );
}
