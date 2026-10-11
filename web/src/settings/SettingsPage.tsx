import { Link, useParams, useSearchParams } from "react-router";
import { errorText } from "../api/errors";
import { useMe } from "../auth/hooks";
import { actorFor, useProject } from "../projects/hooks";
import { isSystemAdmin } from "../projects/logic";
import { AuditTab } from "./AuditTab";
import { GeneralTab } from "./GeneralTab";
import { SETTINGS_TABS, tabFromParam, visibleTabs } from "./logic";
import { MembersTab } from "./MembersTab";
import { NotificationsTab } from "./NotificationsTab";
import { TelegramTab } from "./TelegramTab";
import { TrashTab } from "./TrashTab";

/** Настройки проекта (ТЗ §7.9): шесть вкладок, текущая — в адресе (?tab=…). */
export function SettingsPage() {
  const key = useParams().key ?? "";
  const [params, setParams] = useSearchParams();
  const me = useMe();
  const project = useProject(key);
  const actor = actorFor(me.data, project.data);
  const admin = isSystemAdmin(me.data);
  const visible = visibleTabs({ manager: !!actor?.manager, admin });
  const tab = tabFromParam(params.get("tab"), visible);

  const p = project.data;
  // В архивном проекте менять нельзя ничего (§3.2), кроме разархивации администратором.
  // Удалённый проект сюда не попадает: GET /projects/{key} отдаёт 404, восстановление — в списке проектов.
  const readOnly = !!p?.is_archived;

  let body;
  if (project.isError || me.isError) body = <p role="alert">{errorText(project.error ?? me.error)}</p>;
  else if (!p || !me.data) body = <p className="muted">Загрузка…</p>;
  else if (!tab) body = <p className="notice">Настройки проекта доступны менеджеру проекта и администратору системы.</p>;
  else
    body = (
      <>
        <nav className="tabs settings-tabs" role="tablist" aria-label="Разделы настроек">
          {SETTINGS_TABS.filter((t) => visible.includes(t.id)).map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={tab === t.id}
              className={tab === t.id ? "tab active" : "tab"}
              onClick={() => setParams({ tab: t.id }, { replace: true })}
            >
              {t.label}
            </button>
          ))}
        </nav>
        {p.is_archived && <p className="notice">Проект в архиве: настройки только для чтения.</p>}
        {/* key: при переходе в другой проект формы вкладок начинаются заново, а не с чужими значениями. */}
        <div className="settings-body" role="tabpanel" key={key}>
          {tab === "general" && <GeneralTab project={p} admin={admin} />}
          {tab === "members" && <MembersTab projectKey={key} me={me.data} admin={admin} readOnly={readOnly} />}
          {tab === "telegram" && <TelegramTab project={p} />}
          {tab === "notifications" && <NotificationsTab project={p} readOnly={readOnly} />}
          {tab === "audit" && <AuditTab projectKey={key} />}
          {tab === "trash" && <TrashTab projectKey={key} readOnly={readOnly} />}
        </div>
      </>
    );

  return (
    <section className="settings-page">
      <div className="block-head">
        <h2>
          <span className="card-key">{key}</span> {p?.name} — настройки
        </h2>
        <Link to={`/projects/${key}`}>← К доске</Link>
      </div>
      {body}
    </section>
  );
}
