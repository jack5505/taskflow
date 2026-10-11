-- Объём спринта на старте (ТЗ §3.8, §5.2): задачи и их оценки в момент запуска, не меняется.
CREATE TABLE sprint_start_scope (
    sprint_id        uuid    NOT NULL REFERENCES sprints (id),
    task_id          uuid    NOT NULL REFERENCES tasks (id),
    estimate_minutes integer NOT NULL CHECK (estimate_minutes >= 0),  -- без оценки — 0
    PRIMARY KEY (sprint_id, task_id)
);

-- §5: в проекте не больше одного активного спринта — последняя линия защиты под блокировкой проекта.
CREATE UNIQUE INDEX sprints_one_active_idx ON sprints (project_id) WHERE state = 'active';
