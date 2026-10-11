package sprint

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SnapshotEvery — как часто воркер проверяет, не наступила ли полночь в поясе какого-то проекта.
// Пояса проектов разные (и бывают со сдвигом на 30 и 45 минут), поэтому не таймер на 00:00 сервера,
// а частая проверка «снимок за вчера по поясу проекта уже есть?».
const SnapshotEvery = time.Minute

// snapshotStats — состояние задач спринта s для снимка (§3.8): незакрытые (§5.4: todo, in_progress,
// review) и сумма их оценок (без оценки — 0), готовые. Удалённые не считаются.
const snapshotStats = `SELECT count(*) FILTER (WHERE t.status IN ('todo', 'in_progress', 'review')) AS open_tasks,
		COALESCE(SUM(t.estimate_minutes) FILTER (WHERE t.status IN ('todo', 'in_progress', 'review')), 0) AS open_estimate_minutes,
		count(*) FILTER (WHERE t.status = 'done') AS done_tasks
	FROM tasks t WHERE t.sprint_id = s.id AND t.deleted_at IS NULL`

// Snapshot — ежедневные снимки (§3.8): для каждого активного спринта, у которого в поясе его
// проекта уже наступил новый день, записывает состояние на конец вчерашнего дня. Идемпотентно:
// снимок за дату один (ключ sprint_id + date), повторный вызов в тот же день ничего не пишет.
// Дни до start_date не снимаются. Возвращает число новых снимков.
func Snapshot(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	return snapshotYesterday(db.WithContext(ctx), now, nil)
}

// snapshotYesterday — снимок за вчера по поясу проекта для активных спринтов (only — только этот).
// Спринты, у которых снимок за вчера уже есть, отсеиваются до подсчёта задач: воркер ходит раз
// в минуту, и почти всегда делать ему нечего.
func snapshotYesterday(db *gorm.DB, now time.Time, only *uuid.UUID) (int64, error) {
	res := db.Exec(`INSERT INTO sprint_daily_snapshots (sprint_id, date, open_tasks, open_estimate_minutes, done_tasks)
		SELECT s.id, d.day, c.open_tasks, c.open_estimate_minutes, c.done_tasks
		FROM sprints s JOIN projects p ON p.id = s.project_id AND p.deleted_at IS NULL
		CROSS JOIN LATERAL (SELECT (?::timestamptz AT TIME ZONE p.timezone)::date - 1 AS day) d
		CROSS JOIN LATERAL (`+snapshotStats+`) c
		WHERE s.state = 'active' AND d.day >= s.start_date AND (CAST(? AS uuid) IS NULL OR s.id = ?)
			AND NOT EXISTS (SELECT 1 FROM sprint_daily_snapshots x WHERE x.sprint_id = s.id AND x.date = d.day)
		ON CONFLICT (sprint_id, date) DO NOTHING`, now, only, only)
	if res.Error != nil {
		return 0, fmt.Errorf("sprint: snapshot: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// snapshotOnClose — снимок в момент закрытия (§3.8) на сегодняшнюю дату по поясу проекта, в
// транзакции закрытия и до переноса незакрытых задач. Заменяет снимок того же дня, если он был.
// Если спринт закрывают сразу после полуночи, раньше прохода воркера, снимок за вчера делается здесь же.
func snapshotOnClose(tx *gorm.DB, sprintID uuid.UUID, now time.Time) error {
	if _, err := snapshotYesterday(tx, now, &sprintID); err != nil {
		return err
	}
	if err := tx.Exec(`INSERT INTO sprint_daily_snapshots (sprint_id, date, open_tasks, open_estimate_minutes, done_tasks)
		SELECT s.id, (?::timestamptz AT TIME ZONE p.timezone)::date, c.open_tasks, c.open_estimate_minutes, c.done_tasks
		FROM sprints s JOIN projects p ON p.id = s.project_id
		CROSS JOIN LATERAL (`+snapshotStats+`) c
		WHERE s.id = ?
		ON CONFLICT (sprint_id, date) DO UPDATE SET open_tasks = EXCLUDED.open_tasks,
			open_estimate_minutes = EXCLUDED.open_estimate_minutes, done_tasks = EXCLUDED.done_tasks, updated_at = now()`,
		now, sprintID).Error; err != nil {
		return fmt.Errorf("sprint: close snapshot: %w", err)
	}
	return nil
}

// RunSnapshots — цикл воркера: Snapshot при старте и затем раз в SnapshotEvery, пока ctx жив.
func RunSnapshots(ctx context.Context, db *gorm.DB) {
	run := func() {
		jobCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if n, err := Snapshot(jobCtx, db, time.Now()); err != nil {
			slog.Error("sprint snapshots", "err", err)
		} else if n > 0 {
			slog.Info("sprint snapshots", "created", n)
		}
	}
	run()
	t := time.NewTicker(SnapshotEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
