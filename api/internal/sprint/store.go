package sprint

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/task"
)

// Name — название спринта id (для карточки задачи, §8.3); нет такого — "".
func Name(db *gorm.DB, id uuid.UUID) (string, error) {
	var name string
	if err := db.Raw(`SELECT name FROM sprints WHERE id = ?`, id).Scan(&name).Error; err != nil {
		return "", fmt.Errorf("sprint: name: %w", err)
	}
	return name, nil
}

// Active — активный спринт проекта.
type Active struct {
	ID                 uuid.UUID
	ProjectID          uuid.UUID
	Name               string
	StartDate, EndDate *time.Time
}

// ActiveOf — активный спринт проекта projectID (если их несколько — самый ранний); нет — nil.
func ActiveOf(db *gorm.DB, projectID uuid.UUID) (*Active, error) {
	var sp []Active
	if err := db.Raw(`SELECT id, project_id, name, start_date, end_date FROM sprints WHERE project_id = ? AND state = 'active'
		ORDER BY start_date, id LIMIT 1`, projectID).Scan(&sp).Error; err != nil {
		return nil, fmt.Errorf("sprint: active: %w", err)
	}
	if len(sp) == 0 {
		return nil, nil
	}
	return &sp[0], nil
}

// ActiveIn — активные спринты проектов projectIDs.
func ActiveIn(db *gorm.DB, projectIDs []uuid.UUID) ([]Active, error) {
	var sp []Active
	if err := db.Raw(`SELECT id, project_id, name, start_date, end_date FROM sprints WHERE project_id IN ? AND state = 'active'`,
		projectIDs).Scan(&sp).Error; err != nil {
		return nil, fmt.Errorf("sprint: active: %w", err)
	}
	return sp, nil
}

// projectSprints — спринты проекта: активный, запланированные по дате начала, затем закрытые
// (новые первыми).
func projectSprints(db *gorm.DB, projectID uuid.UUID) ([]Sprint, error) {
	var rows []Sprint
	if err := db.Raw(`SELECT * FROM sprints WHERE project_id = ?
		ORDER BY CASE state WHEN 'active' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END,
			CASE WHEN state = 'closed' THEN NULL ELSE start_date END NULLS LAST,
			closed_at DESC NULLS LAST, created_at`, projectID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: list: %w", err)
	}
	return rows, nil
}

// sprintProject — проект спринта id; нет — nil.
func sprintProject(db *gorm.DB, id uuid.UUID) (*uuid.UUID, error) {
	var ids []uuid.UUID
	if err := db.Raw(`SELECT project_id FROM sprints WHERE id = ?`, id).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("sprint: find: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}

// sprintByID — спринт id; нет — nil.
func sprintByID(tx *gorm.DB, id uuid.UUID) (*Sprint, error) {
	var rows []Sprint
	if err := tx.Raw(`SELECT * FROM sprints WHERE id = ?`, id).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: view: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// lockSprints — спринты ids, строки FOR UPDATE в порядке id.
func lockSprints(tx *gorm.DB, ids []uuid.UUID) ([]Sprint, error) {
	var rows []Sprint
	if err := tx.Raw(`SELECT * FROM sprints WHERE id IN ? ORDER BY id FOR UPDATE`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: lock: %w", err)
	}
	return rows, nil
}

// sprintNames — названия всех спринтов проекта.
func sprintNames(tx *gorm.DB, projectID uuid.UUID) ([]string, error) {
	var names []string
	if err := tx.Model(&Sprint{}).Where("project_id = ?", projectID).Pluck("name", &names).Error; err != nil {
		return nil, fmt.Errorf("sprint: names: %w", err)
	}
	return names, nil
}

// startScopeTotals — объём спринта на старте (§5.2).
func startScopeTotals(db *gorm.DB, id uuid.UUID) (Scope, error) {
	var sc Scope
	if err := db.Raw(`SELECT count(*) AS tasks, COALESCE(SUM(estimate_minutes), 0) AS estimate_minutes,
			count(*) FILTER (WHERE estimate_minutes = 0) AS without_estimate
		FROM sprint_start_scope WHERE sprint_id = ?`, id).Scan(&sc).Error; err != nil {
		return Scope{}, fmt.Errorf("sprint: report start: %w", err)
	}
	return sc, nil
}

// lastSnapshotOpen — незакрытые задачи и их оценка из последнего снимка спринта; снимков нет — nil.
func lastSnapshotOpen(db *gorm.DB, id uuid.UUID) (*Totals, error) {
	var sn []Totals
	if err := db.Raw(`SELECT open_tasks AS tasks, open_estimate_minutes AS estimate_minutes FROM sprint_daily_snapshots
		WHERE sprint_id = ? ORDER BY date DESC LIMIT 1`, id).Scan(&sn).Error; err != nil {
		return nil, fmt.Errorf("sprint: report current: %w", err)
	}
	if len(sn) == 0 {
		return nil, nil
	}
	return &sn[0], nil
}

// sprintLog — журнал спринта по времени (§5.3); Key и Title не заполнены.
func sprintLog(db *gorm.DB, id uuid.UUID) ([]Change, error) {
	var changes []Change
	if err := db.Raw(`SELECT task_id, user_id AS "by", created_at AS "at", action
		FROM sprint_log WHERE sprint_id = ? ORDER BY created_at, id`, id).Scan(&changes).Error; err != nil {
		return nil, fmt.Errorf("sprint: report log: %w", err)
	}
	return changes, nil
}

// scopeAndLogTasks — задачи объёма на старте и журнала спринта без повторов.
func scopeAndLogTasks(db *gorm.DB, id uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if err := db.Raw(`SELECT task_id FROM sprint_start_scope WHERE sprint_id = @s
		UNION SELECT task_id FROM sprint_log WHERE sprint_id = @s`, map[string]any{"s": id}).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("sprint: report spent: %w", err)
	}
	return ids, nil
}

// snapshotPoint — снимок спринта за дату.
type snapshotPoint struct {
	Date                time.Time
	OpenTasks           int
	OpenEstimateMinutes int
	DoneTasks           int
}

// snapshotsBetween — снимки спринта за даты from..to.
func snapshotsBetween(db *gorm.DB, id uuid.UUID, from, to task.Date) ([]snapshotPoint, error) {
	var snaps []snapshotPoint
	if err := db.Raw(`SELECT date, open_tasks, open_estimate_minutes, done_tasks
		FROM sprint_daily_snapshots WHERE sprint_id = ? AND date BETWEEN ? AND ?`, id, from, to).Scan(&snaps).Error; err != nil {
		return nil, fmt.Errorf("sprint: report snapshots: %w", err)
	}
	return snaps, nil
}

// snapshotSprint — активный спринт для снимка.
type snapshotSprint struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	StartDate *time.Time
}

// activeForSnapshot — активные спринты (only — только этот).
func activeForSnapshot(db *gorm.DB, only *uuid.UUID) ([]snapshotSprint, error) {
	var rows []snapshotSprint
	if err := db.Raw(`SELECT id, project_id, start_date FROM sprints
		WHERE state = 'active' AND (CAST(? AS uuid) IS NULL OR id = ?)`, only, only).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: snapshot: %w", err)
	}
	return rows, nil
}

// takenSnapshots — уже записанные снимки спринтов ids за даты days (ГГГГ-ММ-ДД), без статистики.
func takenSnapshots(db *gorm.DB, ids []uuid.UUID, days []string) ([]snapshotRow, error) {
	var rows []snapshotRow
	if err := db.Raw(`SELECT sprint_id, date::text AS date FROM sprint_daily_snapshots WHERE sprint_id IN ? AND date IN ?`, ids, days).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: snapshot: %w", err)
	}
	return rows, nil
}

// writeSnapshots пишет снимки rows одним запросом; replace — заменить снимок той же даты, иначе
// существующий не трогается. Возвращает число записанных.
func writeSnapshots(db *gorm.DB, rows []snapshotRow, replace bool) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	conflict := `DO NOTHING`
	if replace {
		conflict = `DO UPDATE SET open_tasks = EXCLUDED.open_tasks,
			open_estimate_minutes = EXCLUDED.open_estimate_minutes, done_tasks = EXCLUDED.done_tasks, updated_at = now()`
	}
	values := make([]string, len(rows))
	args := make([]any, 0, 5*len(rows))
	for i, r := range rows {
		values[i] = "(?, ?, ?, ?, ?)"
		args = append(args, r.SprintID, r.Date, r.OpenTasks, r.OpenEstimateMinutes, r.DoneTasks)
	}
	res := db.Exec(`INSERT INTO sprint_daily_snapshots (sprint_id, date, open_tasks, open_estimate_minutes, done_tasks)
		VALUES `+strings.Join(values, ", ")+` ON CONFLICT (sprint_id, date) `+conflict, args...)
	if res.Error != nil {
		return 0, fmt.Errorf("sprint: snapshot: %w", res.Error)
	}
	return res.RowsAffected, nil
}
