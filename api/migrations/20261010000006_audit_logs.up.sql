-- Журнал действий менеджеров и администраторов (ТЗ §3.8). Записи не редактируются и не удаляются:
-- это обеспечивает триггер, а не только отсутствие API.
CREATE TABLE audit_logs (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id    uuid        REFERENCES users (id),               -- NULL — команда на сервере (source = cli) или система
    project_id  uuid        REFERENCES projects (id),            -- NULL — действие вне проекта (пользователи)
    action      text        NOT NULL,
    entity_type text        NOT NULL,
    entity_id   text        NOT NULL,
    old_value   jsonb,
    new_value   jsonb,
    source      text        NOT NULL CHECK (source IN ('web', 'bot', 'cli', 'system')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Журнал проекта по периоду (§9.2, 7.9) с курсорной пагинацией по (created_at, id).
CREATE INDEX audit_logs_project_idx ON audit_logs (project_id, created_at DESC, id DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_id, created_at DESC);

CREATE FUNCTION audit_logs_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs: записи журнала не изменяются и не удаляются';
END;
$$;

CREATE TRIGGER audit_logs_immutable BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_immutable();
