package task

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Функции для модуля sprint: задачи спринтов — для карточек спринта, снимков (§3.8), закрытия
// (§5.4) и отчёта (§5.6). Удалённые задачи не считаются, если не сказано иное.

// SprintTotal — число неудалённых задач спринта и сумма их оценок (без оценки — 0).
type SprintTotal struct {
	Tasks           int
	EstimateMinutes int
}

// SprintTotalsOf — SprintTotal каждого спринта из sprintIDs; спринта без задач в ответе нет.
func SprintTotalsOf(db *gorm.DB, sprintIDs []uuid.UUID) (map[uuid.UUID]SprintTotal, error) {
	out := make(map[uuid.UUID]SprintTotal, len(sprintIDs))
	if len(sprintIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SprintID uuid.UUID
		SprintTotal
	}
	if err := db.Raw(`SELECT sprint_id, count(*) AS tasks, COALESCE(SUM(estimate_minutes), 0) AS estimate_minutes
		FROM tasks WHERE sprint_id IN ? AND deleted_at IS NULL GROUP BY sprint_id`, sprintIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: sprint totals: %w", err)
	}
	for _, r := range rows {
		out[r.SprintID] = r.SprintTotal
	}
	return out, nil
}

// CloseTotals — задачи спринта при закрытии (§5.4): без удалённых и отменённых — готовые, всего,
// сумма оценок и списанного.
type CloseTotals struct {
	Done            int
	Total           int
	EstimateMinutes int
	SpentMinutes    int
}

// CloseTotalsOf — CloseTotals спринта sprintID; строки задач вызывающий уже заблокировал (LockSprintTasks).
func CloseTotalsOf(tx *gorm.DB, sprintID uuid.UUID) (*CloseTotals, error) {
	var c CloseTotals
	if err := tx.Raw(`SELECT count(*) FILTER (WHERE status = 'done') AS done, count(*) AS total,
			COALESCE(SUM(estimate_minutes), 0) AS estimate_minutes, COALESCE(SUM(spent_minutes), 0) AS spent_minutes
		FROM tasks WHERE sprint_id = ? AND deleted_at IS NULL AND status <> 'cancelled'`, sprintID).Scan(&c).Error; err != nil {
		return nil, fmt.Errorf("task: sprint close totals: %w", err)
	}
	return &c, nil
}

// SnapshotStats — состояние задач спринта для ежедневного снимка (§3.8): незакрытые (§5.4: todo,
// in_progress, review) и сумма их оценок (без оценки — 0), готовые.
type SnapshotStats struct {
	OpenTasks           int
	OpenEstimateMinutes int
	DoneTasks           int
}

// SnapshotStatsOf — SnapshotStats каждого спринта из sprintIDs; у спринта без задач — нули.
func SnapshotStatsOf(db *gorm.DB, sprintIDs []uuid.UUID) (map[uuid.UUID]SnapshotStats, error) {
	out := make(map[uuid.UUID]SnapshotStats, len(sprintIDs))
	if len(sprintIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		SprintID uuid.UUID
		SnapshotStats
	}
	if err := db.Raw(`SELECT sprint_id,
			count(*) FILTER (WHERE status IN ('todo', 'in_progress', 'review')) AS open_tasks,
			COALESCE(SUM(estimate_minutes) FILTER (WHERE status IN ('todo', 'in_progress', 'review')), 0) AS open_estimate_minutes,
			count(*) FILTER (WHERE status = 'done') AS done_tasks
		FROM tasks WHERE sprint_id IN ? AND deleted_at IS NULL GROUP BY sprint_id`, sprintIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: sprint snapshot stats: %w", err)
	}
	for _, r := range rows {
		out[r.SprintID] = r.SnapshotStats
	}
	return out, nil
}

// LockSprintTasks блокирует FOR UPDATE все задачи спринтов sprintIDs (и удалённые) в порядке id.
// Порядок блокировок модуля sprint: спринт → его задачи (здесь) → проект — тот же, что у правок
// задач (задача → проект), поэтому встречные транзакции не встают в deadlock.
func LockSprintTasks(tx *gorm.DB, sprintIDs []uuid.UUID) error {
	if err := tx.Exec(`SELECT id FROM tasks WHERE sprint_id IN ? ORDER BY id FOR UPDATE`, sprintIDs).Error; err != nil {
		return fmt.Errorf("task: lock sprint tasks: %w", err)
	}
	return nil
}

// ScopeTask — задача спринта на его старте (§5.2) с оценкой (nil — без оценки).
type ScopeTask struct {
	ID              uuid.UUID
	EstimateMinutes *int
}

// SprintScope — неудалённые задачи спринта sprintID; строки вызывающий уже заблокировал.
func SprintScope(tx *gorm.DB, sprintID uuid.UUID) ([]ScopeTask, error) {
	var rows []ScopeTask
	if err := tx.Raw(`SELECT id, estimate_minutes FROM tasks WHERE sprint_id = ? AND deleted_at IS NULL`, sprintID).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: sprint scope: %w", err)
	}
	return rows, nil
}

// IDsInSprint — id всех задач спринта sprintID, в том числе удалённых.
func IDsInSprint(db *gorm.DB, sprintID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if err := db.Raw(`SELECT id FROM tasks WHERE sprint_id = ?`, sprintID).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("task: sprint task ids: %w", err)
	}
	return ids, nil
}

// Titled — ключ и название задачи.
type Titled struct {
	ID    uuid.UUID
	Key   string
	Title string
}

// LiveTitles — ключи и названия неудалённых задач из ids по id; удалённых в ответе нет.
func LiveTitles(db *gorm.DB, ids []uuid.UUID) (map[uuid.UUID]Titled, error) {
	out := make(map[uuid.UUID]Titled, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []Titled
	if err := db.Raw(`SELECT id, key, title FROM tasks WHERE id IN ? AND deleted_at IS NULL`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: titles: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// UserMinutes — списанное пользователем время в минутах.
type UserMinutes struct {
	UserID  uuid.UUID
	Minutes int
}

// SpentByUser — записи времени по неудалённым задачам из taskIDs за дни from..until включительно
// (until == nil — без верхней границы), суммы по пользователям; порядок не задан.
func SpentByUser(db *gorm.DB, taskIDs []uuid.UUID, from time.Time, until *time.Time) ([]UserMinutes, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	var untilDate *string
	if until != nil {
		d := until.Format(time.DateOnly)
		untilDate = &d
	}
	var rows []UserMinutes
	if err := db.Raw(`SELECT w.user_id, SUM(w.minutes) AS minutes
		FROM worklogs w JOIN tasks t ON t.id = w.task_id AND t.deleted_at IS NULL
		WHERE w.task_id IN ? AND w.work_date >= ? AND (CAST(? AS date) IS NULL OR w.work_date <= ?)
		GROUP BY w.user_id`, taskIDs, from.Format(time.DateOnly), untilDate, untilDate).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: spent by user: %w", err)
	}
	return rows, nil
}
