import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { api } from "../api";
import type { MeProfile, ProjectMemberView } from "../api/client";
import { errorText, fieldErrors, parseApiError } from "../api/errors";
import { membersQueryKey, projectQueryKey, useProjectMembers } from "../projects/hooks";
import { projectsQueryKey } from "../projects/logic";
import { usersQueryKey, useUsers } from "./hooks";
import { memberErrorText } from "./logic";

type Role = "manager" | "member";

const ROLES: { value: Role; label: string }[] = [
  { value: "member", label: "Исполнитель" },
  { value: "manager", label: "Менеджер" },
];

interface Props {
  projectKey: string;
  me: MeProfile;
  admin: boolean;
  readOnly: boolean;
}

/**
 * Участники (§7.9): роли, добавление существующего пользователя, исключение. Администратор
 * системы здесь же заводит пользователя (§7.1) и деактивирует его во всей системе (§3.1).
 */
export function MembersTab({ projectKey, me, admin, readOnly }: Props) {
  const members = useProjectMembers(projectKey);
  const [error, setError] = useState("");
  const qc = useQueryClient();
  const navigate = useNavigate();

  const refresh = () => {
    qc.invalidateQueries({ queryKey: membersQueryKey(projectKey) });
    // Своя роль могла измениться — от неё зависят права на экранах проекта.
    qc.invalidateQueries({ queryKey: projectQueryKey(projectKey) });
  };

  const setRole = useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: Role }) =>
      api.api.v1ProjectsMembersUpdate(projectKey, userId, { role }),
    onMutate: () => setError(""),
    onSuccess: refresh,
    onError: (e) => setError(memberErrorText(e, "role")),
  });

  const remove = useMutation({
    mutationFn: (userId: string) => api.api.v1ProjectsMembersDelete(projectKey, userId),
    onMutate: () => setError(""),
    onSuccess: (_, userId) => {
      // Менеджер исключил себя: проект ему больше не виден (404) — на список проектов.
      if (userId === me.id && !admin) {
        qc.invalidateQueries({ queryKey: projectsQueryKey });
        navigate("/");
        return;
      }
      refresh();
    },
    onError: (e) => setError(memberErrorText(e, "remove")),
  });

  const setActive = useMutation({
    mutationFn: ({ userId, active }: { userId: string; active: boolean }) =>
      api.api.v1UsersPartialUpdate(userId, { is_active: active }),
    onMutate: () => setError(""),
    onSuccess: () => {
      refresh();
      qc.invalidateQueries({ queryKey: usersQueryKey });
    },
    onError: (e) => setError(memberErrorText(e, "deactivate")),
  });

  const busy = setRole.isPending || remove.isPending || setActive.isPending;
  const editable = !readOnly;
  const list = members.data ?? [];

  return (
    <>
      {members.isError && <p role="alert">{errorText(members.error)}</p>}
      {error && (
        <p role="alert" className="board-error">
          {error}
        </p>
      )}
      <table className="table">
        <thead>
          <tr>
            <th>Имя</th>
            <th>Email</th>
            <th>Роль</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {list.map((m) => (
            <tr key={m.user_id}>
              <td>
                {m.display_name}
                {m.is_active === false && <span className="badge">деактивирован</span>}
              </td>
              <td>{m.email}</td>
              <td>
                {editable ? (
                  <select
                    aria-label={`Роль: ${m.display_name}`}
                    value={m.role}
                    disabled={busy}
                    onChange={(e) => setRole.mutate({ userId: m.user_id ?? "", role: e.target.value as Role })}
                  >
                    {ROLES.map((r) => (
                      <option key={r.value} value={r.value}>
                        {r.label}
                      </option>
                    ))}
                  </select>
                ) : (
                  ROLES.find((r) => r.value === m.role)?.label
                )}
              </td>
              <td className="row-actions">
                {editable && (
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() =>
                      window.confirm(`Исключить ${m.display_name} из проекта?`) && remove.mutate(m.user_id ?? "")
                    }
                  >
                    Исключить
                  </button>
                )}
                {admin && m.user_id !== me.id && (
                  <DeactivateButton member={m} busy={busy} onToggle={(active) => setActive.mutate({ userId: m.user_id ?? "", active })} />
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {editable && <AddMember projectKey={projectKey} members={list} onAdded={refresh} />}
      {editable && admin && <CreateUser projectKey={projectKey} onCreated={refresh} />}
    </>
  );
}

function DeactivateButton({
  member,
  busy,
  onToggle,
}: {
  member: ProjectMemberView;
  busy: boolean;
  onToggle: (active: boolean) => void;
}) {
  if (member.is_active === false)
    return (
      <button type="button" disabled={busy} onClick={() => onToggle(true)}>
        Активировать
      </button>
    );
  return (
    <button
      type="button"
      className="danger"
      disabled={busy}
      onClick={() =>
        window.confirm(
          `Деактивировать ${member.display_name} во всей системе? Пользователь не сможет войти, его таймер ` +
            "остановится, открытые задачи освободятся, привязка Telegram снимется.",
        ) && onToggle(false)
      }
    >
      Деактивировать
    </button>
  );
}

/** Добавить существующего активного пользователя (менеджеру GET /users отдаёт только активных). */
function AddMember({
  projectKey,
  members,
  onAdded,
}: {
  projectKey: string;
  members: ProjectMemberView[];
  onAdded: () => void;
}) {
  const users = useUsers();
  const [userId, setUserId] = useState("");
  const [role, setRole] = useState<Role>("member");
  const add = useMutation({
    mutationFn: () => api.api.v1ProjectsMembersUpdate(projectKey, userId, { role }),
    onSuccess: () => {
      setUserId("");
      onAdded();
    },
  });
  const inProject = new Set(members.map((m) => m.user_id));
  // Администратору список приходит целиком — деактивированных добавить нельзя.
  const candidates = (users.data ?? []).filter((u) => !inProject.has(u.id) && u.is_active !== false);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (userId) add.mutate();
  };

  return (
    <form className="form panel" onSubmit={submit}>
      <h3>Добавить участника</h3>
      <div className="actions">
        <select aria-label="Пользователь" value={userId} onChange={(e) => setUserId(e.target.value)}>
          <option value="">Выберите пользователя…</option>
          {candidates.map((u) => (
            <option key={u.id} value={u.id}>
              {u.display_name} ({u.email})
            </option>
          ))}
        </select>
        <select aria-label="Роль" value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {ROLES.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
        <button type="submit" className="primary" disabled={!userId || add.isPending}>
          Добавить
        </button>
      </div>
      {users.isError && <p role="alert">{errorText(users.error)}</p>}
      {add.isError && <p role="alert">{errorText(add.error)}</p>}
    </form>
  );
}

/**
 * Новый пользователь (только администратор системы): POST /users отправляет письмо со ссылкой
 * установки пароля (§7.1); созданный сразу добавляется в проект с выбранной ролью.
 */
function CreateUser({ projectKey, onCreated }: { projectKey: string; onCreated: () => void }) {
  const qc = useQueryClient();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [done, setDone] = useState("");
  const create = useMutation({
    mutationFn: async () => {
      const u = (await api.api.v1UsersCreate({ email: email.trim(), display_name: name.trim() })).data;
      qc.invalidateQueries({ queryKey: usersQueryKey });
      try {
        await api.api.v1ProjectsMembersUpdate(projectKey, u.id ?? "", { role });
        return `Пользователь ${u.email} создан и добавлен в проект, письмо для установки пароля отправлено.`;
      } catch (e) {
        // Пользователь уже заведён — повторное создание упадёт на занятом email; добавить его
        // можно формой «Добавить участника».
        return `Пользователь ${u.email} создан, но не добавлен в проект: ${errorText(e)}`;
      }
    },
    onSuccess: (text) => {
      setDone(text);
      setEmail("");
      setName("");
      onCreated();
    },
  });
  const fields = fieldErrors(parseApiError(create.error));
  const reason = (f: string) => fields.find((e) => e.field === f)?.reason;
  const generalError = create.isError && fields.length === 0 ? errorText(create.error) : "";

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setDone("");
    create.mutate();
  };

  return (
    <form className="form panel" onSubmit={submit}>
      <h3>Новый пользователь</h3>
      <label>
        Email
        <input type="email" required maxLength={254} value={email} onChange={(e) => setEmail(e.target.value)} />
        {reason("email") && <span role="alert">{reason("email")}</span>}
      </label>
      <label>
        Имя
        <input required maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
        {reason("display_name") && <span role="alert">{reason("display_name")}</span>}
      </label>
      <label>
        Роль в проекте
        <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {ROLES.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
      </label>
      <span className="hint">Пользователю придёт письмо со ссылкой для установки пароля (действует 24 часа).</span>
      {generalError && <p role="alert">{generalError}</p>}
      {done && <p className="notice">{done}</p>}
      <div className="actions">
        <button type="submit" className="primary" disabled={create.isPending}>
          {create.isPending ? "Создаём…" : "Создать"}
        </button>
      </div>
    </form>
  );
}
