package sprint

import (
	"fmt"

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
