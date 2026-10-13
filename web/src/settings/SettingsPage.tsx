import { useParams, useSearchParams } from "react-router";
import { useMe } from "../auth/hooks";
import { actorFor, useProject } from "../projects/hooks";
import { isSystemAdmin } from "../projects/logic";
import { AuditTab } from "./AuditTab";
import { GeneralTab } from "./GeneralTab";
import { GitHubTab } from "./GitHubTab";
import { SETTINGS_TABS, tabFromParam, visibleTabs } from "./logic";
import { MembersTab } from "./MembersTab";
import { NotificationsTab } from "./NotificationsTab";
import { TelegramTab } from "./TelegramTab";
import { TrashTab } from "./TrashTab";
import { ProjectHeader } from "../projects/ProjectHeader";
import { LoadError } from "../ui/LoadError";
import { Tabs, tabPanel } from "../ui/Tabs";

/** Настройки проекта (ТЗ §7.9, §12.1): шесть вкладок и «GitHub», текущая — в адресе (?tab=…). */
export function SettingsPage() {
  const key = useParams().key ?? "";
  const [params, setParams] = useSearchParams();
  const me = useMe();
  const project = useProject(key);
  const actor = actorFor(me.data, project.data);
  const admin = isSystemAdmin(me.data);
  const visible = visibleTabs({ manager: !!actor?.manager, admin, github: !!me.data?.github_enabled });
  const tab = tabFromParam(params.get("tab"), visible);

  const p = project.data;
  // В архивном проекте менять нельзя ничего (§3.2), кроме разархивации администратором.
  // Удалённый проект сюда не попадает: GET /projects/{key} отдаёт 404, восстановление — в списке проектов.
  const readOnly = !!p?.is_archived;

  let body;
  if (project.isError || me.isError)
    body = (
      <LoadError error={project.error ?? me.error} onRetry={() => [project, me].forEach((q) => q.isError && void q.refetch())} />
    );
  else if (!p || !me.data) body = <p className="muted">Загрузка…</p>;
  else if (!tab) body = <p className="notice">Настройки проекта доступны менеджеру проекта и администратору системы.</p>;
  else
    body = (
      <>
        <Tabs
          label="Разделы настроек"
          className="settings-tabs"
          idPrefix="settings"
          tabs={SETTINGS_TABS.filter((t) => visible.includes(t.id))}
          value={tab}
          onChange={(id) => setParams({ tab: id }, { replace: true })}
        />
        {p.is_archived && <p className="notice">Проект в архиве: настройки только для чтения.</p>}
        {/* key: при переходе в другой проект формы вкладок начинаются заново, а не с чужими значениями. */}
        <div className="settings-body" {...tabPanel("settings", tab)} key={key}>
          {tab === "general" && <GeneralTab project={p} admin={admin} />}
          {tab === "members" && <MembersTab projectKey={key} me={me.data} admin={admin} readOnly={readOnly} />}
          {tab === "telegram" && <TelegramTab project={p} readOnly={readOnly} />}
          {tab === "github" && <GitHubTab project={p} readOnly={readOnly} />}
          {tab === "notifications" && <NotificationsTab project={p} readOnly={readOnly} />}
          {tab === "audit" && <AuditTab projectKey={key} />}
          {tab === "trash" && <TrashTab projectKey={key} readOnly={readOnly} />}
        </div>
      </>
    );

  return (
    <section className="settings-page">
      <ProjectHeader projectKey={key} />
      {body}
    </section>
  );
}
