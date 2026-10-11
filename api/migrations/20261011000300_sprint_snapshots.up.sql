-- Ежедневные снимки спринта (ТЗ §3.8, §5.6): состояние активного спринта на конец дня по поясу
-- проекта (воркер в 00:00) и в момент закрытия. Берндаун строится по ним. Один снимок на дату:
-- повторный проход воркера ничего не дублирует, снимок закрытия заменяет снимок того же дня.
CREATE TABLE sprint_daily_snapshots (
    sprint_id             uuid        NOT NULL REFERENCES sprints (id),
    date                  date        NOT NULL,                                  -- день по поясу проекта
    open_tasks            integer     NOT NULL CHECK (open_tasks >= 0),            -- todo, in_progress, review
    open_estimate_minutes integer     NOT NULL CHECK (open_estimate_minutes >= 0), -- без оценки — 0
    done_tasks            integer     NOT NULL CHECK (done_tasks >= 0),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sprint_id, date)
);
