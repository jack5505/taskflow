import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { ProjectCreatedInvite, ProjectInviteView } from "../api/client";
import { errorText } from "../api/errors";
import { useTimeZone } from "../auth/hooks";
import { formatDate } from "../lib/format";
import { useConfirm } from "../ui/Confirm";
import { LoadError } from "../ui/LoadError";
import { useToast } from "../ui/Toast";
import { ROLES, roleLabel, type Role } from "./logic";


const invitesQueryKey = (key: string) => ["project-invites", key] as const;

/**
 * Ссылки-приглашения (§7.1, §7.9): менеджер создаёт ссылку с ролью, человек по ней сам
 * регистрируется и попадает в проект. Полная ссылка видна только сразу после создания —
 * сервер хранит лишь хеш токена.
 */
export function InvitesPanel({ projectKey }: { projectKey: string }) {
  const toast = useToast();
  const confirm = useConfirm();
  const qc = useQueryClient();
  const timeZone = useTimeZone();
  const [role, setRole] = useState<Role>("member");
  const [created, setCreated] = useState<ProjectCreatedInvite | null>(null);

  const invites = useQuery({
    queryKey: invitesQueryKey(projectKey),
    queryFn: async ({ signal }): Promise<ProjectInviteView[]> =>
      (await api.api.v1ProjectsInvitesList(projectKey, { signal })).data,
  });
  const refresh = () => qc.invalidateQueries({ queryKey: invitesQueryKey(projectKey) });

  const create = useMutation({
    mutationFn: async () => (await api.api.v1ProjectsInvitesCreate(projectKey, { role })).data,
    onSuccess: (inv) => {
      setCreated(inv);
      refresh();
    },
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api.api.v1ProjectsInvitesDelete(projectKey, id),
    onSuccess: (_, id) => {
      if (created?.id === id) setCreated(null);
      refresh();
      toast.success("Ссылка отозвана");
    },
    onError: (e) => toast.error(errorText(e)),
  });

  const copy = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url);
      toast.success("Ссылка скопирована");
    } catch {
      toast.error("Не удалось скопировать: выделите ссылку и скопируйте вручную");
    }
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  const list = invites.data ?? [];

  return (
    <section className="form panel invites-panel" aria-labelledby="invites-title">
      <h3 id="invites-title">Ссылки-приглашения</h3>
      <p className="muted">
        По ссылке человек сам вводит имя, email и пароль и сразу попадает в проект с выбранной ролью. Ссылка действует 7
        дней, по ней может присоединиться несколько человек.
      </p>
      <form className="actions" onSubmit={submit}>
        <select aria-label="Роль по ссылке" value={role} onChange={(e) => setRole(e.target.value as Role)}>
          {ROLES.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
        <button type="submit" className="primary" disabled={create.isPending}>
          {create.isPending ? "Создаём…" : "Создать ссылку"}
        </button>
      </form>
      {create.isError && <p role="alert">{errorText(create.error)}</p>}
      {created?.url && (
        <div className="invite-created">
          <label>
            Новая ссылка ({roleLabel(created.role)}) — скопируйте её сейчас: потом полностью она не показывается
            <input readOnly value={created.url} onFocus={(e) => e.currentTarget.select()} />
          </label>
          <button type="button" onClick={() => void copy(created.url ?? "")}>
            Копировать
          </button>
        </div>
      )}

      {invites.isError && <LoadError error={invites.error} onRetry={() => void invites.refetch()} />}
      {list.length > 0 && (
        <table className="table" aria-label="Действующие ссылки">
          <thead>
            <tr>
              <th>Роль</th>
              <th>Создал</th>
              <th>Действует до</th>
              <th>Присоединились</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.map((inv) => (
              <tr key={inv.id}>
                <td>{roleLabel(inv.role)}</td>
                <td>{inv.created_by_name || "—"}</td>
                <td>{formatDate(inv.expires_at, timeZone)}</td>
                <td>{inv.joined_count ?? 0}</td>
                <td className="row-actions">
                  <button
                    type="button"
                    disabled={revoke.isPending}
                    onClick={async () =>
                      (await confirm({
                        title: "Отозвать ссылку?",
                        body: "По ней больше никто не присоединится. Уже вступившие останутся в проекте.",
                        confirmLabel: "Отозвать",
                        danger: true,
                      })) && revoke.mutate(inv.id ?? "")
                    }
                  >
                    Отозвать
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
