package sprint

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// SnapshotEvery — как часто воркер проверяет, не наступила ли полночь в поясе какого-то проекта.
// Пояса проектов разные (и бывают со сдвигом на 30 и 45 минут), поэтому не таймер на 00:00 сервера,
// а частая проверка «снимок за вчера по поясу проекта уже есть?».
const SnapshotEvery = time.Minute

// Snapshot — ежедневные снимки (§3.8): для каждого активного спринта, у которого в поясе его
// проекта уже наступил новый день, записывает состояние на конец вчерашнего дня. Идемпотентно:
// снимок за дату один (ключ sprint_id + date), повторный вызов в тот же день ничего не пишет.
// Дни до start_date не снимаются. Возвращает число новых снимков.
func Snapshot(ctx context.Context, db *gorm.DB, now time.Time) (int64, error) {
	return snapshotYesterday(db.WithContext(ctx), now, nil)
}

// snapshotRow — строка sprint_daily_snapshots.
type snapshotRow struct {
	SprintID uuid.UUID
	Date     string
	task.SnapshotStats
}

// snapshotYesterday — снимок за вчера по поясу проекта для активных спринтов неудалённых
// проектов (only — только этот). Спринты, у которых снимок за вчера уже есть, отсеиваются до
// подсчёта задач: воркер ходит раз в минуту, и почти всегда делать ему нечего.
func snapshotYesterday(db *gorm.DB, now time.Time, only *uuid.UUID) (int64, error) {
	active, err := activeForSnapshot(db, only)
	if err != nil {
		return 0, err
	}
	if len(active) == 0 {
		return 0, nil
	}
	projectIDs := make([]uuid.UUID, len(active))
	for i, sp := range active {
		projectIDs[i] = sp.ProjectID
	}
	zones, err := project.Timezones(db, projectIDs, false)
	if err != nil {
		return 0, err
	}
	var due []snapshotRow
	var ids []uuid.UUID
	var days []string
	for _, sp := range active {
		tz, ok := zones[sp.ProjectID]
		if !ok || sp.StartDate == nil {
			continue // проект удалён; без даты начала спринт не снимается
		}
		today, err := dateIn(now, tz)
		if err != nil {
			return 0, err
		}
		day := today.AddDate(0, 0, -1)
		if day.Before(*sp.StartDate) {
			continue
		}
		d := day.Format(time.DateOnly)
		due, ids = append(due, snapshotRow{SprintID: sp.ID, Date: d}), append(ids, sp.ID)
		if !slices.Contains(days, d) {
			days = append(days, d)
		}
	}
	if len(due) == 0 {
		return 0, nil
	}
	taken, err := takenSnapshots(db, ids, days)
	if err != nil {
		return 0, err
	}
	done := make(map[snapshotRow]bool, len(taken))
	for _, t := range taken {
		done[t] = true
	}
	rows := due[:0]
	ids = ids[:0]
	for _, r := range due {
		if !done[r] {
			rows, ids = append(rows, r), append(ids, r.SprintID)
		}
	}
	return insertSnapshots(db, rows, ids, false)
}

// insertSnapshots пишет снимки rows с текущим состоянием задач их спринтов (ids). replace —
// заменить снимок той же даты, иначе существующий не трогается. Возвращает число записанных.
func insertSnapshots(db *gorm.DB, rows []snapshotRow, ids []uuid.UUID, replace bool) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	stats, err := task.SnapshotStatsOf(db, ids)
	if err != nil {
		return 0, err
	}
	full := make([]snapshotRow, len(rows)) // строки вызывающего не меняются
	for i, r := range rows {
		r.SnapshotStats = stats[r.SprintID]
		full[i] = r
	}
	return writeSnapshots(db, full, replace)
}

// snapshotOnClose — снимок в момент закрытия (§3.8) на сегодняшнюю дату по поясу проекта, в
// транзакции закрытия и до переноса незакрытых задач. Заменяет снимок того же дня, если он был.
// Если спринт закрывают сразу после полуночи, раньше прохода воркера, снимок за вчера делается здесь же.
func snapshotOnClose(tx *gorm.DB, sp *Sprint, timezone string, now time.Time) error {
	if _, err := snapshotYesterday(tx, now, &sp.ID); err != nil {
		return err
	}
	today, err := dateIn(now, timezone)
	if err != nil {
		return err
	}
	_, err = insertSnapshots(tx, []snapshotRow{{SprintID: sp.ID, Date: today.Format(time.DateOnly)}}, []uuid.UUID{sp.ID}, true)
	return err
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
