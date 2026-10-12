// Package sprintdb — запросы модуля sprint к таблицам sprints и sprint_log, которые нужны модулю
// task. Отдельный пакет без зависимостей от task: sprint импортирует task, и task не может
// импортировать sprint без цикла. Владелец таблиц тот же — модуль sprint (api/CLAUDE.md).
package sprintdb

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Состояния спринта §3.4; sprint.StateX — те же значения.
const (
	StatePlanned = "planned"
	StateActive  = "active"
	StateClosed  = "closed"
)

// LockShare блокирует FOR SHARE строки спринтов ids в порядке id. Модуль task берёт их раньше
// строк задач и проекта — тот же порядок, что у закрытия спринта (спринт → задачи → проект).
func LockShare(tx *gorm.DB, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	var got []uuid.UUID
	if err := tx.Raw(`SELECT id FROM sprints WHERE id IN ? ORDER BY id FOR SHARE`, ids).Scan(&got).Error; err != nil {
		return fmt.Errorf("sprint: lock share: %w", err)
	}
	return nil
}

// State — состояние спринта id; нет такого — "". Блокировок не берёт.
func State(db *gorm.DB, id uuid.UUID) (string, error) {
	var states []string
	if err := db.Raw(`SELECT state FROM sprints WHERE id = ?`, id).Scan(&states).Error; err != nil {
		return "", fmt.Errorf("sprint: state: %w", err)
	}
	if len(states) == 0 {
		return "", nil
	}
	return states[0], nil
}

// StateIn — состояние спринта id проекта projectID; спринта нет в проекте — "". Блокировок не берёт.
func StateIn(db *gorm.DB, projectID, id uuid.UUID) (string, error) {
	var states []string
	if err := db.Raw(`SELECT state FROM sprints WHERE id = ? AND project_id = ?`, id, projectID).Scan(&states).Error; err != nil {
		return "", fmt.Errorf("sprint: state: %w", err)
	}
	if len(states) == 0 {
		return "", nil
	}
	return states[0], nil
}

// ActiveID — активный спринт проекта (§5: не больше одного; если вдруг несколько — как sprint.ActiveOf,
// самый ранний); нет — nil. Блокировок не берёт.
func ActiveID(db *gorm.DB, projectID uuid.UUID) (*uuid.UUID, error) {
	var ids []uuid.UUID
	if err := db.Raw(`SELECT id FROM sprints WHERE project_id = ? AND state = 'active' ORDER BY start_date, id LIMIT 1`, projectID).
		Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("sprint: active: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}

// Log пишет в журнал спринта (§5.3), что задачу добавили или убрали (action: added, removed).
func Log(tx *gorm.DB, sprintID, taskID uuid.UUID, by *uuid.UUID, action string) error {
	if err := tx.Exec(`INSERT INTO sprint_log (sprint_id, task_id, user_id, action) VALUES (?, ?, ?, ?)`,
		sprintID, taskID, by, action).Error; err != nil {
		return fmt.Errorf("sprint: log: %w", err)
	}
	return nil
}

// Active — активный спринт проекта.
type Active struct {
	ID                 uuid.UUID
	ProjectID          uuid.UUID
	Name               string
	StartDate, EndDate *time.Time
}

// ActiveOfProjects — активный спринт каждого проекта из projectIDs (§5: один; если вдруг
// несколько — самый ранний по start_date, затем id); без активного — нет в ответе.
func ActiveOfProjects(db *gorm.DB, projectIDs []uuid.UUID) (map[uuid.UUID]Active, error) {
	out := make(map[uuid.UUID]Active, len(projectIDs))
	if len(projectIDs) == 0 {
		return out, nil
	}
	var rows []Active
	if err := db.Raw(`SELECT DISTINCT ON (project_id) id, project_id, name, start_date, end_date FROM sprints
		WHERE project_id IN ? AND state = 'active' ORDER BY project_id, start_date, id`, projectIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sprint: active: %w", err)
	}
	for _, r := range rows {
		out[r.ProjectID] = r
	}
	return out, nil
}
