package sprint

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
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
