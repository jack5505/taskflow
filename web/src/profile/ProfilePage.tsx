import { useMemo, useState, type FormEvent } from "react";
import type { MeProfile } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { useMe } from "../auth/hooks";
import { timeZones } from "../projects/logic";
import { useLogoutAll, usePasswordLink, useUpdateMe } from "./hooks";
import {
  PERSONAL_EVENTS,
  changedNotifications,
  displayNameError,
  effectiveNotifications,
  isKnownTimeZone,
  profileChanges,
} from "./logic";
import { TelegramBlock } from "./TelegramBlock";

/** Профиль (ТЗ §7.8): имя и часовой пояс, пароль и сессии, Telegram, личные уведомления §8.7. */
export function ProfilePage() {
  const me = useMe();

  let body;
  if (me.isError) body = <p role="alert">{errorText(me.error)}</p>;
  else if (!me.data) body = <p className="muted">Загрузка…</p>;
  else
    body = (
      <>
        <AccountForm me={me.data} />
        <SecurityBlock me={me.data} />
        <TelegramBlock me={me.data} />
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
  const [saved, setSaved] = useState(false);
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
    setSaved(false);
    if (update.isError) update.reset();
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (displayNameError(name) || !isKnownTimeZone(timezone, zones)) return;
    const body = profileChanges(base, name, timezone);
    if (Object.keys(body).length === 0) return setSaved(true);
    update.mutate(body, {
      onSuccess: (fresh) => {
        // Форма — с тем, что сохранил сервер (имя он обрезает).
        const next = { display_name: fresh.display_name ?? "", timezone: fresh.timezone ?? "" };
        setBase(next);
        setName(next.display_name);
        setTimezone(next.timezone);
        setSaved(true);
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
        {saved && <span className="muted">Сохранено</span>}
      </div>
    </form>
  );
}

/**
 * Пароль и сессии (§7.8, §9.1). Пароль меняется по ссылке из письма (в API нет смены с вводом
 * текущего); после установки нового пароля сервер отзывает все сессии. «Выйти везде» отзывает
 * сессии сразу, в том числе эту.
 */
function SecurityBlock({ me }: { me: MeProfile }) {
  const password = usePasswordLink();
  const logoutAll = useLogoutAll();
  const email = me.email ?? "";

  const passwordError = password.isError
    ? (fieldErrors(parseApiError(password.error)).find((f) => f.field === "email")?.reason ??
      errorText(password.error, "Не удалось отправить письмо, попробуйте ещё раз"))
    : "";

  return (
    <section className="panel">
      <h3>Пароль и сеансы</h3>
      {password.isSuccess ? (
        <p className="notice">
          Письмо со ссылкой отправлено на {email}. Ссылка действует 24 часа; после установки нового пароля потребуется
          войти заново на всех устройствах. Если письма нет, проверьте спам и повторите не раньше чем через минуту.
        </p>
      ) : (
        <p className="muted">Для смены пароля пришлём на {email} ссылку, по которой задаётся новый пароль.</p>
      )}
      {passwordError && <p role="alert">{passwordError}</p>}
      {logoutAll.isError && <p role="alert">{errorText(logoutAll.error, "Не удалось выйти на всех устройствах")}</p>}
      <div className="actions">
        <button
          type="button"
          disabled={password.isPending || !email}
          onClick={() =>
            window.confirm(`Отправить ссылку для смены пароля на ${email}?`) && password.mutate(email)
          }
        >
          {password.isPending ? "Отправляем…" : "Сменить пароль"}
        </button>
        <button
          type="button"
          className="danger"
          disabled={logoutAll.isPending}
          onClick={() =>
            window.confirm("Завершить все сеансы, включая этот? Потребуется войти заново на всех устройствах.") &&
            logoutAll.mutate()
          }
        >
          Выйти везде
        </button>
      </div>
    </section>
  );
}

/**
 * Личные уведомления §8.7: по умолчанию все включены, отправляются только переключатели,
 * изменённые в форме (база — как в AccountForm: состояние при открытии или после сохранения).
 */
function NotificationsForm({ me }: { me: MeProfile }) {
  const [base, setBase] = useState(() => effectiveNotifications(me.notification_settings));
  const [events, setEvents] = useState(base);
  const [saved, setSaved] = useState(false);
  const update = useUpdateMe();

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const changes = changedNotifications(base, events);
    if (Object.keys(changes).length === 0) return setSaved(true);
    update.mutate(
      { notification_settings: changes },
      {
        onSuccess: (fresh) => {
          const next = effectiveNotifications(fresh.notification_settings);
          setBase(next);
          setEvents(next);
          setSaved(true);
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
                setSaved(false);
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
        {saved && <span className="muted">Сохранено</span>}
      </div>
    </form>
  );
}
